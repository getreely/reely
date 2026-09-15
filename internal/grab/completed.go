package grab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/naming"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/quality"
)

// Completed-download handling: SAB's history is the work queue. Every
// finished job in a reely category gets matched back to its title, moved
// into the library under the naming template, attached, recorded — and then
// deleted from SAB's history so it never comes around again. Failed jobs
// get recorded and cleared the same way.

// SettingsReader is the sliver of settings the import path needs.
type SettingsReader interface {
	Get(key string) string
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	".mov": true, ".wmv": true, ".mpg": true, ".mpeg": true, ".ts": true,
}

// retryBackoff is how long a job that failed to import waits before the
// next attempt — long enough not to hammer, short enough to recover.
const retryBackoff = 10 * time.Minute

// importedMemory is how long reely remembers filing a job. It has to
// outlast the retry backoff comfortably — the whole failure mode is a
// history entry that reappears after the backoff — while still letting SAB
// reuse an id eventually.
const importedMemory = 6 * time.Hour

// importProblem is a completed download whose import keeps failing — the
// Activity page shows these with their reason, plus retry and hand-resolve.
type importProblem struct {
	NzoID string `json:"nzoId"`
	Name  string `json:"name"`
	Error string `json:"error"`
	next  time.Time
}

// ImportCompleted sweeps SAB's history for reely's categories and imports
// every completed job. Returns how many files were imported.
func (s *Service) ImportCompleted(ctx context.Context) int {
	if !s.haveClient() {
		return 0
	}
	imported := 0
	// where files landed this sweep, so the media server can be pointed
	// at those folders rather than asked to re-read everything
	landed := map[string]bool{}
	s.mu.Lock()
	s.landedIn = landed
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.landedIn = nil
		dirs := make([]string, 0, len(landed))
		for dir := range landed {
			dirs = append(dirs, dir)
		}
		s.mu.Unlock()
		if len(dirs) == 0 || s.Placed == nil {
			return
		}
		sort.Strings(dirs)
		s.Placed(dirs)
	}()
	// both categories may be configured to the same value — sweep each once
	categories := []string{s.movieCategory()}
	if tv := s.tvCategory(); tv != categories[0] {
		categories = append(categories, tv)
	}
	for _, category := range categories {
		// every configured client: an install running both protocols has
		// finished jobs waiting in each, and sweeping one would leave the
		// other's imports to pile up unnoticed
		items, err := s.finished(ctx, category)
		if err != nil {
			log.Printf("reely: download history poll (%s): %v", category, err)
			continue
		}
		for _, item := range items {
			// already filed, whatever SAB now says about it: retry the
			// clear and move on, never re-import and never record a
			// failure for a download that already landed
			if s.alreadyImported(item.ID) {
				s.clearJob(ctx, item.ID, item.Protocol)
				continue
			}
			if s.deferred(item.ID) {
				continue
			}
			switch item.Status {
			case download.StatusCompleted:
				n, err := s.importJob(ctx, item)
				if err != nil {
					log.Printf("reely: import %q: %v", item.Name, err)
					s.deferRetry(item.ID, item.Name, err)
					continue
				}
				imported += n
			case download.StatusFailed:
				// the failure is recorded AGAINST the title it was grabbed
				// for. Filing it with no ids leaves the dead grab as that
				// title's newest event, and the pending-grab gate then
				// blocks every automatic search for it for three days —
				// invisibly, since manual search ignores that gate.
				movieID, showID, episodeID, err := s.Catalog.GrabTarget(item.ID)
				if err != nil {
					log.Printf("reely: failed job %q: reading its grab target: %v", item.Name, err)
					movieID, showID, episodeID = 0, 0, 0
				}
				detail, _ := json.Marshal(map[string]string{"title": item.Name, "error": item.FailMessage})
				if err := s.Catalog.AddHistory("failed", movieID, showID, episodeID, string(detail)); err != nil {
					log.Printf("reely: record failure %q: %v", item.Name, err)
				}
				// a failed release is banned from automatic re-grabbing —
				// the Activity blocklist tab is the pardon
				s.blocklistFailed(item)
				// with the failed release banned, an immediate re-search
				// grabs the next best one instead of waiting for a sweep
				s.retryFailed(item)
				s.clearJob(ctx, item.ID, item.Protocol)
			}
			// anything else (Queued never appears here; Verifying/Extracting
			// do) is still in flight — leave it for the next tick
		}
	}
	return imported
}

// importJob moves one completed job's video files into the library and
// attaches them. Season packs carry several files; each imports on its own.
// The job's own name decides movie vs episode — categories only scope the
// SAB sweep, so movies and TV sharing one category still sort correctly.
func (s *Service) importJob(ctx context.Context, item download.HistoryItem) (int, error) {
	if item.Storage == "" {
		return 0, fmt.Errorf("no storage path in SAB history")
	}
	files, err := videoFiles(item.Storage)
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, fmt.Errorf("no video files under %s", item.Storage)
	}
	jobParse := parser.Parse(item.Name)

	// a job reely grabbed itself imports onto the title it was grabbed
	// for — the release had its hearing at grab time, and an obfuscated
	// name can't derail it. The name keeps one veto: parsing confidently
	// to a DIFFERENT title marks a mislabeled release, which stops here
	// for a human call in Activity instead of filing under either guess.
	movieID, showID, episodeID, err := s.Catalog.GrabTarget(item.ID)
	if err != nil {
		return 0, err
	}
	if movieID > 0 || showID > 0 {
		n, err := s.importGrabbed(item, files, jobParse, movieID, showID, episodeID)
		if err != nil {
			return n, err
		}
		s.clearJob(ctx, item.ID, item.Protocol)
		return n, nil
	}

	imported := 0
	if jobParse.Kind == "movie" {
		// a movie job is its single largest file
		path := files[0]
		if err := s.importMovieFile(path, jobParse, item); err != nil {
			return imported, err
		}
		imported++
	} else {
		seen := map[string]bool{}
		for _, path := range files {
			if err := ctx.Err(); err != nil {
				return imported, err
			}
			if err := s.importEpisodeFile(path, jobParse, item, seen); err != nil {
				return imported, err
			}
			imported++
		}
	}
	s.clearJob(ctx, item.ID, item.Protocol)
	return imported, nil
}

// importGrabbed files a job onto the title reely grabbed it for.
func (s *Service) importGrabbed(item download.HistoryItem, files []string, jobParse parser.Result,
	movieID, showID, episodeID int64) (int, error) {
	if movieID > 0 {
		m, err := s.Catalog.GetMovie(movieID)
		if err != nil {
			return 0, fmt.Errorf("the movie this was grabbed for is gone: %w", err)
		}
		path := files[0]
		fileParse := parser.Parse(filepath.Base(path))
		for _, p := range []parser.Result{jobParse, fileParse} {
			if reason := mislabelVeto(m.Title, m.Aliases, p); reason != "" {
				return 0, errors.New(reason)
			}
		}
		qual := firstNonEmpty(fileParse.Quality, jobParse.Quality)
		qsrc := firstNonEmpty(fileParse.Source, jobParse.Source)
		if err := s.placeMovieFile(&m.Movie, path, qual, qsrc, item.Name, item.Protocol); err != nil {
			return 0, err
		}
		return 1, nil
	}

	sh, err := s.Catalog.GetShow(showID)
	if err != nil {
		return 0, fmt.Errorf("the show this was grabbed for is gone: %w", err)
	}
	if reason := mislabelVeto(sh.Title, sh.Aliases, jobParse); reason != "" {
		return 0, errors.New(reason)
	}
	imported := 0
	seen := map[string]bool{}
	for _, path := range files {
		p := parser.Parse(filepath.Base(path))
		if reason := mislabelVeto(sh.Title, sh.Aliases, p); reason != "" {
			return imported, errors.New(reason)
		}
		if p.Kind != "episode" && (jobParse.Kind == "episode" || jobParse.Kind == "season") {
			p = jobParse
		}
		if p.Kind != "episode" {
			// a job name that declares seasons vouches for its files'
			// compact numbering
			packCtx := jobParse.Kind == "season" || jobParse.Kind == "episode"
			if q, ok := combinedEpisode(sh, filepath.Base(path), packCtx); ok {
				// compact scene numbering ("Show.301." = S03E01), verified
				// against the show's own episodes — already catalog numbering
				p = q
			} else if episodeID > 0 && len(files) == 1 {
				// no markers anywhere — a single-episode grab still knows
				// exactly which episode it went hunting for
				season, episode, err := s.Catalog.EpisodeNumbers(episodeID)
				if err != nil {
					return imported, err
				}
				p = parser.Result{Kind: "episode"}
				p.Ep.Season, p.Ep.Episode, p.Ep.EpisodeEnd = season, episode, episode
			} else {
				return imported, fmt.Errorf("cannot tell which episode %s is", filepath.Base(path))
			}
		} else {
			// the name speaks scene numbering; the grab-target path above is
			// already in the show's own
			p = fileToCatalog(sh, p)
		}
		if seen[spanKey(p)] {
			log.Printf("reely: %q: %s repeats episodes already imported from this job, skipped",
				item.Name, filepath.Base(path))
			continue
		}
		seen[spanKey(p)] = true
		qual := firstNonEmpty(p.Quality, jobParse.Quality)
		qsrc := firstNonEmpty(p.Source, jobParse.Source)
		if err := s.placeShowFile(sh, path, p, qual, qsrc, item.Name, item.Protocol); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// mislabelVeto vetoes an import whose release name parses CONFIDENTLY to
// a different title than it was grabbed for. Confidence needs structure —
// a year, quality, source, or episode marker; obfuscated names (bare
// hashes) carry none, so the grab linkage carries them instead. Returns
// the reason, or "" to proceed.
func mislabelVeto(target string, aliases []string, p parser.Result) string {
	// the tolerant comparison: a release carrying a country tag the title
	// lacks ("Kitchen Nightmares US"), or an alias name (an anthology
	// season's own subtitle), is the same title, not a mislabel
	if p.Title == "" || titleMatchRelease(p.Title, target) {
		return ""
	}
	for _, a := range aliases {
		if titleMatchRelease(p.Title, a) {
			return ""
		}
	}
	structured := p.Year > 0 || p.Quality != "" || p.Source != "" || p.Kind == "episode" || p.Kind == "season"
	if !structured {
		return ""
	}
	return fmt.Sprintf("release looks like %q but was grabbed for %q — if the file is right, resolve it onto the title by hand", p.Title, target)
}

func (s *Service) importMovieFile(path string, jobParse parser.Result, item download.HistoryItem) error {
	m, err := s.findMovie(jobParse.Title, jobParse.Year)
	if err != nil {
		return err
	}
	fileParse := parser.Parse(filepath.Base(path))
	qual := firstNonEmpty(fileParse.Quality, jobParse.Quality)
	qsrc := firstNonEmpty(fileParse.Source, jobParse.Source)
	return s.placeMovieFile(m, path, qual, qsrc, item.Name, item.Protocol)
}

// placeMovieFile moves one file into a movie's library slot under the
// naming template, attaches it, and records the import. A file already on
// the library gets replaced — and deleted once nothing references it. Shared
// by the automatic sweep, hand-resolution, and uploads.
// noteLanded records the folder a file just landed in, for the sweep to
// hand to Placed when it finishes.
func (s *Service) noteLanded(dest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.landedIn != nil {
		s.landedIn[filepath.Dir(dest)] = true
	}
}

// placeInto puts a downloaded file into a library, in the way its
// protocol demands.
//
// This is the whole difference between the two. A usenet job is over
// when it finishes: the file is reely's to move, and what is left in the
// job folder is residue. A torrent is not over — it seeds, and it seeds
// the very file that was just downloaded. Moving that file out from
// under it stops the seeding and can have qBittorrent re-download the
// data it no longer finds.
//
// So a torrent is hard-linked instead. The library gets a name for the
// data, the torrent keeps its own, and the bytes are stored once. Where
// a hard link cannot be made — a download directory and a library on
// different filesystems, which under Docker means separate mounts —
// linkInto copies, which costs a second copy of the file but still
// leaves the torrent whole.
func (s *Service) placeInto(libPath, rel, src, protocol string) (string, int64, error) {
	if protocol == download.Torrent {
		return linkInto(libPath, rel, src)
	}
	return moveInto(libPath, rel, src)
}

func (s *Service) placeMovieFile(m *catalog.Movie, path, qual, qsrc, srcTitle, protocol string) error {
	lib, err := s.Catalog.GetLibrary(m.LibraryID)
	if err != nil {
		return fmt.Errorf("movie %q has no library to import into", m.Title)
	}
	rel := naming.Movie(s.template("naming_movie"), naming.MovieFields{
		Title: m.Title, Year: m.Year, Quality: qual, Source: qsrc,
		TmdbID: m.TmdbID, ImdbID: m.ImdbID,
	})
	oldPath, oldQuality, oldSource := m.FilePath, m.Quality, m.Source
	dest, size, err := s.placeInto(lib.Path, rel, path, protocol)
	if err != nil {
		return err
	}
	s.noteLanded(dest)
	if err := s.Catalog.AttachMovieFile(m.ID, dest, size, qual, qsrc); err != nil {
		return err
	}
	note := map[string]any{"title": srcTitle, "path": dest, "size": size}
	if oldPath != "" && oldPath != dest {
		note["upgraded"] = true
		if oldQuality != "" {
			note["upgradedFrom"] = sourceNote(oldSource, oldQuality)
		}
	}
	detail, _ := json.Marshal(note)
	if err := s.Catalog.AddHistory("imported", m.ID, 0, 0, string(detail)); err != nil {
		return err
	}
	// the same title in other libraries shares this file via hardlinks
	s.fanOutMovie(m, dest, qual, qsrc, srcTitle)
	s.removeReplaced(oldPath, dest)
	return nil
}

func (s *Service) importEpisodeFile(path string, jobParse parser.Result, item download.HistoryItem,
	seen map[string]bool) error {
	// the file's own name carries the episode numbers; the job name is the
	// fallback for single-episode jobs with unhelpful inner file names
	p := parser.Parse(filepath.Base(path))
	if p.Kind != "episode" {
		if jobParse.Kind != "episode" {
			return fmt.Errorf("cannot tell which episode %s is", filepath.Base(path))
		}
		p = jobParse
	}
	// one file per episode per job: files arrive largest first, so whichever
	// claims a span first is the one worth keeping
	if seen[spanKey(p)] {
		log.Printf("reely: %q: %s repeats episodes already imported from this job, skipped",
			item.Name, filepath.Base(path))
		return nil
	}
	seen[spanKey(p)] = true
	title := firstNonEmpty(jobParse.Title, p.Title)
	sh, err := s.findShowForEpisode(title, p)
	if err != nil {
		return err
	}
	p = fileToCatalog(sh, p)
	qual := firstNonEmpty(p.Quality, jobParse.Quality)
	qsrc := firstNonEmpty(p.Source, jobParse.Source)
	return s.placeShowFile(sh, path, p, qual, qsrc, item.Name, item.Protocol)
}

// placeShowFile moves one episode file into a show's library slot,
// attaching it to every episode its name spans. An episode holding a
// strictly better file keeps it — a span import must not stomp a good copy
// while filling its neighbors — and every replaced file is deleted once no
// row references it. Shared by the automatic sweep, hand-resolution, and
// uploads.
func (s *Service) placeShowFile(sh *catalog.ShowDetails, path string, p parser.Result, qual, qsrc, srcTitle, protocol string) error {
	lib, err := s.Catalog.GetLibrary(sh.LibraryID)
	if err != nil {
		return fmt.Errorf("show %q has no library to import into", sh.Title)
	}
	ep := findEpisode(sh, p.Ep.Season, p.Ep.Episode)
	if ep == nil {
		return fmt.Errorf("%s has no S%02dE%02d", sh.Title, p.Ep.Season, p.Ep.Episode)
	}
	rel := naming.Episode(s.template("naming_show"), naming.EpisodeFields{
		Title: sh.Title, Year: sh.Year, Season: ep.Season, Episode: ep.Episode,
		EpisodeTitle: ep.Title, Quality: qual, Source: qsrc,
		TmdbID: sh.TmdbID, TvdbID: sh.TvdbID, ImdbID: sh.ImdbID,
	})
	dest, size, err := s.placeInto(lib.Path, rel, path, protocol)
	if err != nil {
		return err
	}
	// a multi-episode file (S01E01E02) backs every episode it spans
	attached := 0
	replaced := map[string]bool{}
	for n := p.Ep.Episode; n <= p.Ep.EpisodeEnd; n++ {
		target := findEpisode(sh, p.Ep.Season, n)
		if target == nil {
			continue
		}
		if target.FilePath != "" && quality.FileBetter(target.Quality, target.Source, qual, qsrc) {
			continue // its current file is better — keep it
		}
		note := map[string]any{"title": srcTitle, "path": dest, "size": size}
		if target.FilePath != "" && target.FilePath != dest {
			replaced[target.FilePath] = true
			note["upgraded"] = true
			if target.Quality != "" {
				note["upgradedFrom"] = sourceNote(target.Source, target.Quality)
			}
		}
		s.noteLanded(dest)
		if err := s.Catalog.AttachEpisodeFile(target.ID, dest, size, qual, qsrc); err != nil {
			return err
		}
		attached++
		detail, _ := json.Marshal(note)
		if err := s.Catalog.AddHistory("imported", 0, sh.ID, target.ID, string(detail)); err != nil {
			return err
		}
	}
	if attached == 0 {
		// every episode in the span already holds something better — the
		// download was wasted, but the library is right; drop the file quietly
		_ = os.Remove(dest)
		log.Printf("reely: %q: every episode already at better quality, dropped", srcTitle)
		return nil
	}
	// the same show in other libraries shares this file via hardlinks
	s.fanOutShowFile(sh, p, dest, qual, qsrc, srcTitle)
	for old := range replaced {
		s.removeReplaced(old, dest)
	}
	return nil
}

// findMovie / findShow match a parsed release title back to the catalog —
// a grabbed title is already stored, so this never needs TMDB.
func (s *Service) findMovie(title string, year int) (*catalog.Movie, error) {
	movies, err := s.Catalog.ListMovies(0)
	if err != nil {
		return nil, err
	}
	for i := range movies {
		m := &movies[i]
		if titleMatchMovie(title, m) && (year == 0 || m.Year == 0 || absInt(m.Year-year) <= 1) {
			return m, nil
		}
	}
	return nil, fmt.Errorf("no movie in the catalog matches %q (%d)", title, year)
}

func (s *Service) findShow(title string, year int) (*catalog.ShowDetails, error) {
	shows, err := s.Catalog.ListShows(0)
	if err != nil {
		return nil, err
	}
	// exact title first; a release-style match (trailing country tag or
	// year, or an alias) is the fallback so "Kitchen Nightmares US" and an
	// anthology season's subtitle still find their show
	for _, exact := range []bool{true, false} {
		for i := range shows {
			if exact && !titleMatch(shows[i].Title, title) {
				continue
			}
			if !exact && (titleMatch(shows[i].Title, title) || !titleMatchShow(title, &shows[i])) {
				continue
			}
			return s.Catalog.GetShow(shows[i].ID)
		}
	}
	return nil, fmt.Errorf("no show in the catalog matches %q", title)
}

// findShowForEpisode resolves a parsed episode file to a show when several
// share the name — the TMDB-split case, where "Kitchen Nightmares" is two
// entries whose seasons don't overlap. Among title matches, the show that
// actually carries the episode (read through its numbering mapping) wins;
// exact title matches are tried before country-tagged ones.
func (s *Service) findShowForEpisode(title string, p parser.Result) (*catalog.ShowDetails, error) {
	shows, err := s.Catalog.ListShows(0)
	if err != nil {
		return nil, err
	}
	var fallback *catalog.ShowDetails
	for _, exact := range []bool{true, false} {
		for i := range shows {
			if exact && !titleMatch(shows[i].Title, title) {
				continue
			}
			if !exact && (titleMatch(shows[i].Title, title) || !titleMatchShow(title, &shows[i])) {
				continue
			}
			sh, err := s.Catalog.GetShow(shows[i].ID)
			if err != nil {
				continue
			}
			mapped := sceneToCatalog(sh, p)
			if findEpisode(sh, mapped.Ep.Season, mapped.Ep.Episode) != nil {
				return sh, nil
			}
			if fallback == nil {
				fallback = sh
			}
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("no show in the catalog matches %q", title)
}

func (s *Service) template(key string) string {
	if key == "naming_movie" {
		return s.setting(key, "{Title} ({Year})/{Title} ({Year}) [{Quality}]")
	}
	return s.setting(key, "{Title} ({Year})/Season {season:00}/{Title} - S{season:00}E{episode:00} - {Episode Title}")
}

// The SAB categories are the user's to name; the defaults match a plain
// SAB setup. They scope both where grabs are filed and what the import
// sweep watches.
func (s *Service) movieCategory() string { return s.setting("sab_category_movies", "movies") }
func (s *Service) tvCategory() string    { return s.setting("sab_category_tv", "tvshows") }

func (s *Service) setting(key, fallback string) string {
	if s.Settings != nil {
		if v := strings.TrimSpace(s.Settings.Get(key)); v != "" {
			return v
		}
	}
	return fallback
}

// moveInto moves src to libraryPath/relPath (plus src's extension),
// creating folders as needed. Returns the destination and its size.
func moveInto(libraryPath, relPath, src string) (string, int64, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", 0, err
	}
	dest := filepath.Join(libraryPath, relPath+strings.ToLower(filepath.Ext(src)))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Rename(src, dest); err != nil {
		// SAB's download disk and the library are often different volumes —
		// rename can't cross them, so fall back to copy + remove
		if err := copyFile(src, dest); err != nil {
			return "", 0, err
		}
		_ = os.Remove(src)
	}
	return dest, info.Size(), nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dest), ".reely-import-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(out.Name())
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(out.Name())
		return err
	}
	return os.Rename(out.Name(), dest)
}

// videoFiles lists a job folder's videos, largest first.
// spanKey identifies the episodes one file would attach to, so a job
// shipping two files that resolve to the same episode imports the first
// (largest) and skips the rest. Without it the second lands on the same
// episode under the same name and silently replaces the first.
func spanKey(p parser.Result) string {
	return fmt.Sprintf("%d/%d-%d", p.Ep.Season, p.Ep.Episode, p.Ep.EpisodeEnd)
}

// sampleFraction is how small a video must be, relative to the largest in
// the same job, before it counts as a sample rather than as payload. Scene
// samples run a few percent of the real file, while the episodes of a
// season pack run within a factor of two of each other. Judging relatively
// rather than against a fixed size keeps both true whether the release is
// a 4K feature or a twelve-minute cartoon.
const sampleFraction = 0.2

// isSampleName spots the other shape of the same thing: a sample large
// enough to clear the size test usually still says so in its path.
func isSampleName(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, seg := range strings.Split(lower, "/") {
		if seg == "sample" || seg == "samples" || seg == "extras" || seg == "featurettes" {
			return true
		}
	}
	base := filepath.Base(lower)
	return strings.HasPrefix(base, "sample") || strings.Contains(base, "-sample.") ||
		strings.Contains(base, ".sample.")
}

// videoFiles lists a job's real video files, largest first. Samples,
// trailers and extras are left out: they carry the same episode markers as
// the release they ship with, so importing one is not a harmless extra
// file — it resolves to the same episode, renders to the same filename,
// and overwrites the copy that was actually wanted.
func videoFiles(root string) ([]string, error) {
	type sized struct {
		path string
		size int64
	}
	var found []sized
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // unreadable entries are skipped, not fatal
		}
		if !videoExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // same: skip what we cannot stat
		}
		found = append(found, sized{path, info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(found, func(i, j int) bool { return found[i].size > found[j].size })
	if len(found) == 0 {
		return nil, nil
	}
	// the largest file is the yardstick and is always kept, so a job of
	// nothing but samples still imports its best candidate rather than
	// nothing at all
	biggest := found[0].size
	out := []string{found[0].path}
	for _, f := range found[1:] {
		if isSampleName(f.path) {
			continue
		}
		if biggest > 0 && float64(f.size) < float64(biggest)*sampleFraction {
			continue
		}
		out = append(out, f.path)
	}
	return out, nil
}

// deferred / deferRetry keep a failing job from being retried every tick,
// and remember why it failed for the Activity page.
// markImported remembers a job reely has already filed. Clearing it from
// SAB's history can fail — SAB busy, restarting, or having already moved
// the entry — and without this memory the next sweep treats the same job
// as new: it either imports the files a second time, or, if SAB has since
// marked the job failed (which is what SAB reports once its files have
// been moved out from under it), records a failure for a download that
// actually succeeded. That false failure then unblocks the pending gate
// and sends reely hunting for a replacement it does not need.
func (s *Service) markImported(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.importedJobs == nil {
		s.importedJobs = map[string]time.Time{}
	}
	now := time.Now()
	s.importedJobs[id] = now
	// the memory is bounded by time, not by the delete succeeding: SAB can
	// serve an entry back after accepting its deletion, which is precisely
	// the case this exists for
	for id, at := range s.importedJobs {
		if now.Sub(at) > importedMemory {
			delete(s.importedJobs, id)
		}
	}
}

func (s *Service) alreadyImported(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.importedJobs[id]
	return ok && time.Since(at) <= importedMemory
}

func (s *Service) deferred(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.problems[id]
	return ok && time.Now().Before(p.next)
}

func (s *Service) deferRetry(id, name string, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.problems == nil {
		s.problems = map[string]*importProblem{}
	}
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	s.problems[id] = &importProblem{
		NzoID: id, Name: name, Error: msg, next: time.Now().Add(retryBackoff),
	}
}

// Problems lists the imports currently stuck, for the Activity page.
func (s *Service) Problems() []importProblem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]importProblem, 0, len(s.problems))
	for _, p := range s.problems {
		out = append(out, *p)
	}
	return out
}

// RetryNow clears a stuck job's backoff; the next watcher tick retries it.
func (s *Service) RetryNow(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.problems[id]; ok {
		p.next = time.Time{}
	}
}

func (s *Service) clearJob(ctx context.Context, id, protocol string) {
	// recorded before the delete is attempted, not after: the delete is
	// exactly the step that fails, and the memory is what stops a failed
	// delete from becoming a second import
	s.markImported(id)
	// del_files: the video is already moved (or hard-linked) into the
	// library under its own path, so what's left in the job's folder is
	// residue — leaving it accumulated an orphan per handled job
	// A torrent is not finished the way a usenet job is: it is seeding
	// the files it just gave reely, so deleting it would stop that and
	// take its data with it. It is retired instead — moved to a category
	// reely does not sweep — which is also what stops the next pass from
	// importing it all over again, since a torrent never leaves the
	// client's list on its own.
	if protocol == download.Torrent {
		if !s.retire(ctx, id) {
			log.Printf("reely: could not retire torrent %s — it may be imported again", id)
		}
		s.mu.Lock()
		delete(s.problems, id)
		s.mu.Unlock()
		return
	}
	if err := s.actOn(ctx, id, func(c Downloader) error {
		return c.DeleteHistory(ctx, id, true)
	}); err != nil {
		log.Printf("reely: clear download history %s: %v", id, err)
		// deletion failed — back off so the job isn't re-handled every tick
		s.deferRetry(id, "", err)
		return
	}
	s.mu.Lock()
	delete(s.problems, id)
	s.mu.Unlock()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// sourceNote labels a replaced file for the history log — "web 1080p", or
// just the resolution when its source was never known.
func sourceNote(src, q string) string {
	if src == "" {
		return q
	}
	return src + " " + q
}
