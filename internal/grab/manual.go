package grab

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/parser"
)

// The hands-on paths: resolving a stuck download onto the right title, and
// importing files the user uploads directly. Both flow through the same
// place-file machinery as the automatic sweep — naming template, attach,
// history row and all.

// categories are the SAB categories reely files its own downloads under —
// one when movies and shows share it, two when they don't.
func (s *Service) categories() []string {
	categories := []string{s.movieCategory()}
	if tv := s.tvCategory(); tv != categories[0] {
		categories = append(categories, tv)
	}
	return categories
}

// queueScanMax bounds how much of SAB's queue one request will pull.
// Filtering and paging both need the whole list in hand, and SAB has no
// name filter of its own, so the queue is read wholesale and narrowed
// here. A thousand in-flight jobs is far past any real household — and
// twenty times what this used to show.
const queueScanMax = 1000

// Queue returns one page of SAB's in-flight jobs for reely's categories,
// optionally narrowed to those whose name contains filter, plus the total
// the page is cut from.
//
// SAB pages per category, so a single offset can't be handed straight
// through: every category is read, the results merged in category order,
// and the page cut from that. Unfiltered, the total is SAB's own count,
// so it stays exact even past the scan cap.
func (s *Service) Queue(ctx context.Context, filter string, offset, limit int) ([]download.QueueItem, int) {
	if !s.haveClient() {
		return nil, 0
	}
	if offset < 0 {
		offset = 0
	}
	// limit <= 0 means everything that was scanned — what a bulk cancel
	// acting on a whole filtered queue needs
	if limit <= 0 {
		limit = queueScanMax
	}
	var merged []download.QueueItem
	truncated := false
	for _, category := range s.categories() {
		items, err := s.inFlight(ctx, category)
		if err != nil {
			continue
		}
		merged = append(merged, items...)
		// Truncation is "we asked for the cap and got exactly the cap" —
		// never a comparison against SAB's noofslots, which describes the
		// WHOLE queue rather than the category filtered out of it and so
		// reads as "truncated" on every poll of a two-category install.
		truncated = truncated || len(items) >= queueScanMax
	}
	// The total is what was actually collected, never SAB's own count —
	// noofslots across categories counts the same jobs twice, and the
	// pager would offer pages that resolve to nothing.
	total := len(merged)
	if truncated {
		log.Printf("reely: a category holds %d+ jobs — showing the first %d", queueScanMax, total)
	}
	if filter = strings.TrimSpace(filter); filter != "" {
		needle := strings.ToLower(filter)
		kept := make([]download.QueueItem, 0, len(merged))
		for _, it := range merged {
			if strings.Contains(strings.ToLower(it.Name), needle) {
				kept = append(kept, it)
			}
		}
		merged = kept
		total = len(merged)
	}
	if offset >= len(merged) {
		return nil, total
	}
	return merged[offset:min(offset+limit, len(merged))], total
}

// CancelDownload stops a job SAB is still working on and bins its partial
// data. Unlike DeleteJob — which cleans up a *completed* download that
// couldn't be placed — there is no history entry and no final folder yet,
// so SAB itself has to delete what it has written so far.
func (s *Service) CancelDownload(ctx context.Context, id string) error {
	if !s.haveClient() {
		return errors.New("no download client configured")
	}
	if err := s.actOn(ctx, id, func(c Downloader) error {
		return c.DeleteQueue(ctx, id, true)
	}); err != nil {
		return err
	}
	// a cancelled job must not be picked up by the completed sweep if SAB
	// still lists it briefly; marking it imported is the same guard
	// clearJob uses after a delete
	s.markImported(id)
	return nil
}

// PauseDownload holds one in-flight job, and ResumeDownload sets it
// going again.
//
// Which client owns the id is worked out the same way cancelling does:
// an id belongs to at most one of them, so each is tried in turn.
func (s *Service) PauseDownload(ctx context.Context, id string) error {
	if !s.haveClient() {
		return errors.New("no download client configured")
	}
	return s.actOn(ctx, id, func(c Downloader) error { return c.Pause(ctx, id) })
}

// ResumeDownload sets a paused job going again.
func (s *Service) ResumeDownload(ctx context.Context, id string) error {
	if !s.haveClient() {
		return errors.New("no download client configured")
	}
	return s.actOn(ctx, id, func(c Downloader) error { return c.Resume(ctx, id) })
}

// SetDownloadPriority forwards a priority change for one in-flight job —
// SAB's own scale: 2 Force, 1 High, 0 Normal, -1 Low.
func (s *Service) SetDownloadPriority(ctx context.Context, id string, priority int) error {
	if !s.haveClient() {
		return errors.New("no download client configured")
	}
	return s.actOn(ctx, id, func(c Downloader) error {
		return c.SetPriority(ctx, id, priority)
	})
}

// ImportJobAs hand-resolves a stuck completed download onto a chosen title,
// bypassing name matching (which is exactly what failed). Exactly one of
// movieID / showID is set; show files still pick their episodes from their
// own SxxEyy markers.
func (s *Service) ImportJobAs(ctx context.Context, id string, movieID, showID int64, season, episode int) error {
	item, err := s.findHistoryItem(ctx, id)
	if err != nil {
		return err
	}
	files, err := videoFiles(item.Storage)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no video files under %s", item.Storage)
	}
	jobParse := parser.Parse(item.Name)

	if movieID > 0 {
		m, err := s.Catalog.GetMovie(movieID)
		if err != nil {
			return err
		}
		fileParse := parser.Parse(filepath.Base(files[0]))
		qual := firstNonEmpty(fileParse.Quality, jobParse.Quality)
		qsrc := firstNonEmpty(fileParse.Source, jobParse.Source)
		if err := s.placeMovieFile(&m.Movie, files[0], qual, qsrc, item.Name, item.Protocol); err != nil {
			return err
		}
	} else {
		sh, err := s.Catalog.GetShow(showID)
		if err != nil {
			return err
		}
		// an episode pin places one file exactly where the person said;
		// a whole pack pinned to one episode would misfile every file
		if episode > 0 {
			if season <= 0 {
				return errors.New("an episode pin needs its season")
			}
			if len(files) != 1 {
				return fmt.Errorf("this job holds %d video files — an episode pin fits a single file; pick a season or let each file resolve itself", len(files))
			}
			if findEpisode(sh, season, episode) == nil {
				return fmt.Errorf("%s has no S%02dE%02d", sh.Title, season, episode)
			}
			fileParse := parser.Parse(filepath.Base(files[0]))
			p := parser.Result{Kind: "episode"}
			p.Ep.Season, p.Ep.Episode, p.Ep.EpisodeEnd = season, episode, episode
			qual := firstNonEmpty(fileParse.Quality, jobParse.Quality)
			qsrc := firstNonEmpty(fileParse.Source, jobParse.Source)
			if err := s.placeShowFile(sh, files[0], p, qual, qsrc, item.Name, item.Protocol); err != nil {
				return err
			}
			s.clearJob(ctx, id, item.Protocol)
			return nil
		}
		for _, path := range files {
			p := parser.Parse(filepath.Base(path))
			switch p.Kind {
			case "episode":
				p = fileToCatalog(sh, p)
			default:
				// hand-resolved onto this show: the person's choice is the
				// context that lets compact numbering be trusted
				if q, ok := combinedEpisode(sh, filepath.Base(path), true); ok {
					p = q
				} else if jobParse.Kind == "episode" || jobParse.Kind == "season" {
					p = fileToCatalog(sh, jobParse)
				} else {
					return fmt.Errorf("cannot tell which episode %s is", filepath.Base(path))
				}
			}
			// a season pin overrides whatever season the names claimed —
			// the person is telling us where this job's files belong
			if season > 0 {
				p.Ep.Season = season
			}
			qual := firstNonEmpty(p.Quality, jobParse.Quality)
			qsrc := firstNonEmpty(p.Source, jobParse.Source)
			if err := s.placeShowFile(sh, path, p, qual, qsrc, item.Name, item.Protocol); err != nil {
				return err
			}
		}
	}
	s.clearJob(ctx, id, item.Protocol)
	return nil
}

func (s *Service) findHistoryItem(ctx context.Context, id string) (*download.HistoryItem, error) {
	categories := []string{s.movieCategory()}
	if tv := s.tvCategory(); tv != categories[0] {
		categories = append(categories, tv)
	}
	for _, category := range categories {
		items, err := s.finished(ctx, category)
		if err != nil {
			continue
		}
		for i := range items {
			if items[i].ID == id {
				return &items[i], nil
			}
		}
	}
	return nil, fmt.Errorf("no job %s in the download client's history", id)
}

// DeleteJob throws a stuck completed download away entirely: the files
// the download client unpacked and its history entry. The bin, sitting
// next to retry and resolve. The storage path comes from the download
// client's own history — the same path every import reads from.
//
// A torrent is binned by its client rather than by reely, because the
// files are still being seeded from and only qBittorrent can stop that
// before removing them.
func (s *Service) DeleteJob(ctx context.Context, id string) error {
	item, err := s.findHistoryItem(ctx, id)
	if err != nil {
		return err
	}
	// A torrent's files belong to a running torrent, and Storage is the
	// torrent's own content path. Deleting it from underneath
	// qBittorrent leaves a live torrent pointed at files that are no
	// longer there — so the client is asked to do it instead, which
	// stops the torrent first and then removes what it wrote.
	if item.Protocol == download.Torrent {
		client := s.clientFor(download.Torrent)
		if client == nil {
			return errors.New("no torrent client is set up to delete this with")
		}
		return client.DeleteQueue(ctx, id, true)
	}
	if item.Storage != "" {
		if err := os.RemoveAll(item.Storage); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.clearJob(ctx, id, item.Protocol)
	return nil
}

// ImportMovieFromPath imports a file already on the server — or the
// largest video under a folder — into a movie. The copy is hard-linked
// into the library; the source stays exactly where it is.
func (s *Service) ImportMovieFromPath(movieID int64, path string) error {
	m, err := s.Catalog.GetMovie(movieID)
	if err != nil {
		return err
	}
	src, err := pickVideo(path)
	if err != nil {
		return err
	}
	srcParse := parser.Parse(filepath.Base(src))
	old := m.FilePath
	dest, err := s.linkMovieFile(&m.Movie, src, srcParse.Quality, srcParse.Source, filepath.Base(src))
	if err != nil {
		return err
	}
	s.fanOutMovie(&m.Movie, dest, srcParse.Quality, srcParse.Source, filepath.Base(src))
	s.removeReplaced(old, dest)
	return nil
}

// ImportShowFromPath imports server-side files for a show — a single file
// or every video under a folder — each tied to the episode its own name
// carries. Sources stay put; copies hard-link in.
func (s *Service) ImportShowFromPath(showID int64, path string) ([]UploadResult, error) {
	sh, err := s.Catalog.GetShow(showID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	files := []string{path}
	if info.IsDir() {
		if files, err = videoFiles(path); err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("no video files under %s", path)
		}
	}
	out := make([]UploadResult, 0, len(files))
	for _, f := range files {
		name := filepath.Base(f)
		p := parser.Parse(name)
		res := UploadResult{File: name}
		if p.Kind != "episode" {
			res.Error = "no SxxEyy marker in the file name"
			out = append(out, res)
			continue
		}
		p = fileToCatalog(sh, p)
		res.Season, res.Episode = p.Ep.Season, p.Ep.Episode
		dest, err := s.linkShowFile(sh, p, f, p.Quality, p.Source, name)
		switch {
		case err != nil:
			res.Error = err.Error()
		case dest == "":
			res.Error = "already on disk at this quality or better"
		default:
			s.fanOutShowFile(sh, p, dest, p.Quality, p.Source, name)
		}
		out = append(out, res)
	}
	return out, nil
}

// pickVideo resolves an import path to one video file: the path itself,
// or the largest video under a folder.
func pickVideo(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		files, err := videoFiles(path)
		if err != nil {
			return "", err
		}
		if len(files) == 0 {
			return "", fmt.Errorf("no video files under %s", path)
		}
		return files[0], nil
	}
	if !videoExts[strings.ToLower(filepath.Ext(path))] {
		return "", fmt.Errorf("%s is not a video file", filepath.Base(path))
	}
	return path, nil
}

// ImportUploadedMovie imports one user-supplied file for a movie. The
// original filename is only used to read quality off it.
func (s *Service) ImportUploadedMovie(movieID int64, srcPath, origName string) error {
	m, err := s.Catalog.GetMovie(movieID)
	if err != nil {
		return err
	}
	origParse := parser.Parse(origName)
	// an upload is reely's own temporary file: nothing is seeding it, so
	// it moves rather than being linked
	return s.placeMovieFile(&m.Movie, srcPath, origParse.Quality, origParse.Source, origName, "")
}

// UploadResult reports one file of a show upload.
type UploadResult struct {
	File    string `json:"file"`
	Season  int    `json:"season,omitempty"`
	Episode int    `json:"episode,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ImportUploadedShowFiles imports user-supplied files for a show, each tied
// to the episode its own filename names. One bad file doesn't stop the rest.
func (s *Service) ImportUploadedShowFiles(showID int64, files []struct{ Path, Name string }) ([]UploadResult, error) {
	sh, err := s.Catalog.GetShow(showID)
	if err != nil {
		return nil, err
	}
	out := make([]UploadResult, 0, len(files))
	for _, f := range files {
		p := parser.Parse(f.Name)
		res := UploadResult{File: f.Name}
		if p.Kind != "episode" {
			res.Error = "no SxxEyy marker in the file name"
			out = append(out, res)
			continue
		}
		p = fileToCatalog(sh, p)
		res.Season, res.Episode = p.Ep.Season, p.Ep.Episode
		// an upload, as above: reely's own file, so it moves
		if err := s.placeShowFile(sh, f.Path, p, p.Quality, p.Source, f.Name, ""); err != nil {
			res.Error = err.Error()
		}
		out = append(out, res)
	}
	return out, nil
}
