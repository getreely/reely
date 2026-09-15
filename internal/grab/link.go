package grab

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/naming"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/quality"
)

// Hardlink fan-out: the same TMDB title can live in several libraries
// (per-user collections), but one file on disk backs all of them. When a
// file lands for one row — download, upload, or hand-resolve — every
// sibling row missing it gets a hardlink into its own library folder, a
// sibling holding a strictly lower quality gets its copy replaced (one
// title, one file: an upgrade landing anywhere lifts every copy), and when
// a title is added to a new library, an existing sibling file fills it
// instantly. Copy is the cross-filesystem fallback.

// linkInto hardlinks src to libraryPath/relPath (plus src's extension).
// Returns the destination and the file's size.
func linkInto(libraryPath, relPath, src string) (string, int64, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", 0, err
	}
	dest := filepath.Join(libraryPath, relPath+strings.ToLower(filepath.Ext(src)))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Link(src, dest); err != nil {
		if os.IsExist(err) {
			return dest, info.Size(), nil // already linked — idempotent
		}
		// different filesystem (or one that can't hardlink) — copy instead
		if err := copyFile(src, dest); err != nil {
			return "", 0, err
		}
	}
	return dest, info.Size(), nil
}

// fanOutMovie hardlinks a just-placed movie file into every sibling row
// (same TMDB id, other library) that has no file — or a strictly worse
// one. Best-effort: a failed link logs and moves on, it never fails the
// primary import.
func (s *Service) fanOutMovie(primary *catalog.Movie, src, qual, qsrc, srcTitle string) {
	siblings, err := s.Catalog.SiblingMovies(primary.ID)
	if err != nil {
		log.Printf("reely: siblings of %q: %v", primary.Title, err)
		return
	}
	for i := range siblings {
		sib := &siblings[i]
		if sib.LibraryID == 0 {
			continue
		}
		if sib.FilePath != "" && !quality.FileBetter(qual, qsrc, sib.Quality, sib.Source) {
			continue // its copy is as good or better — leave it be
		}
		oldPath := sib.FilePath
		dest, err := s.linkMovieFile(sib, src, qual, qsrc, srcTitle)
		if err != nil {
			log.Printf("reely: link %q into library %d: %v", primary.Title, sib.LibraryID, err)
			continue
		}
		s.removeReplaced(oldPath, dest)
	}
}

// linkMovieFile hardlinks src into one movie row's library and attaches it.
// Returns the link's path.
func (s *Service) linkMovieFile(m *catalog.Movie, src, qual, qsrc, srcTitle string) (string, error) {
	lib, err := s.Catalog.GetLibrary(m.LibraryID)
	if err != nil {
		return "", err
	}
	rel := naming.Movie(s.template("naming_movie"), naming.MovieFields{
		Title: m.Title, Year: m.Year, Quality: qual, Source: qsrc,
		TmdbID: m.TmdbID, ImdbID: m.ImdbID,
	})
	dest, size, err := linkInto(lib.Path, rel, src)
	if err != nil {
		return "", err
	}
	if err := s.Catalog.AttachMovieFile(m.ID, dest, size, qual, qsrc); err != nil {
		return dest, err
	}
	detail, _ := json.Marshal(map[string]any{"title": srcTitle, "path": dest, "size": size, "linked": true})
	return dest, s.Catalog.AddHistory("imported", m.ID, 0, 0, string(detail))
}

// fanOutShowFile hardlinks a just-placed episode file into every sibling
// show row whose matching episodes want it — missing, or below the file's
// quality. linkShowFile gates and cleans up per episode.
func (s *Service) fanOutShowFile(primary *catalog.ShowDetails, p parser.Result, src, qual, qsrc, srcTitle string) {
	ids, err := s.Catalog.SiblingShowIDs(primary.ID)
	if err != nil {
		log.Printf("reely: siblings of %q: %v", primary.Title, err)
		return
	}
	for _, id := range ids {
		sib, err := s.Catalog.GetShow(id)
		if err != nil || sib.LibraryID == 0 {
			continue
		}
		if _, err := s.linkShowFile(sib, p, src, qual, qsrc, srcTitle); err != nil {
			log.Printf("reely: link %q into library %d: %v", primary.Title, sib.LibraryID, err)
		}
	}
}

// linkShowFile hardlinks one episode file into a show row's library,
// attaching every episode the file's name spans that is missing or holds a
// strictly lower quality; replaced files are deleted once unreferenced. A
// span nobody wants is a no-op. Returns the link's path ("" when skipped).
func (s *Service) linkShowFile(sh *catalog.ShowDetails, p parser.Result, src, qual, qsrc, srcTitle string) (string, error) {
	wants := false
	for n := p.Ep.Episode; n <= p.Ep.EpisodeEnd; n++ {
		ep := findEpisode(sh, p.Ep.Season, n)
		if ep != nil && (ep.FilePath == "" || quality.FileBetter(qual, qsrc, ep.Quality, ep.Source)) {
			wants = true
			break
		}
	}
	if !wants {
		return "", nil
	}
	lib, err := s.Catalog.GetLibrary(sh.LibraryID)
	if err != nil {
		return "", err
	}
	ep := findEpisode(sh, p.Ep.Season, p.Ep.Episode)
	if ep == nil {
		return "", nil
	}
	rel := naming.Episode(s.template("naming_show"), naming.EpisodeFields{
		Title: sh.Title, Year: sh.Year, Season: ep.Season, Episode: ep.Episode,
		EpisodeTitle: ep.Title, Quality: qual, Source: qsrc,
		TmdbID: sh.TmdbID, TvdbID: sh.TvdbID, ImdbID: sh.ImdbID,
	})
	dest, size, err := linkInto(lib.Path, rel, src)
	if err != nil {
		return "", err
	}
	for n := p.Ep.Episode; n <= p.Ep.EpisodeEnd; n++ {
		target := findEpisode(sh, p.Ep.Season, n)
		if target == nil {
			continue
		}
		if target.FilePath != "" && !quality.FileBetter(qual, qsrc, target.Quality, target.Source) {
			continue // keep the copy it already has
		}
		oldPath := target.FilePath
		if err := s.Catalog.AttachEpisodeFile(target.ID, dest, size, qual, qsrc); err != nil {
			return dest, err
		}
		detail, _ := json.Marshal(map[string]any{"title": srcTitle, "path": dest, "size": size, "linked": true})
		if err := s.Catalog.AddHistory("imported", 0, sh.ID, target.ID, string(detail)); err != nil {
			return dest, err
		}
		s.removeReplaced(oldPath, dest)
	}
	return dest, nil
}

// LinkMovieFromSiblings fills a freshly-added movie row from a sibling
// that already has the file — the add instantly lands on disk, no
// download needed. Reports whether a file was linked.
func (s *Service) LinkMovieFromSiblings(movieID int64) bool {
	m, err := s.Catalog.GetMovie(movieID)
	if err != nil || m.FilePath != "" || m.LibraryID == 0 {
		return false
	}
	siblings, err := s.Catalog.SiblingMovies(movieID)
	if err != nil {
		return false
	}
	for i := range siblings {
		sib := siblings[i]
		if sib.FilePath == "" {
			continue
		}
		if _, err := s.linkMovieFile(&m.Movie, sib.FilePath, sib.Quality, sib.Source, filepath.Base(sib.FilePath)); err != nil {
			log.Printf("reely: link %q from sibling: %v", m.Title, err)
			continue
		}
		return true
	}
	return false
}

// LinkShowFromSiblings fills a freshly-added show's episodes from sibling
// rows that already have files. Returns how many episodes were linked.
func (s *Service) LinkShowFromSiblings(showID int64) int {
	sh, err := s.Catalog.GetShow(showID)
	if err != nil || sh.LibraryID == 0 {
		return 0
	}
	ids, err := s.Catalog.SiblingShowIDs(showID)
	if err != nil {
		return 0
	}
	linked := 0
	for _, id := range ids {
		sib, err := s.Catalog.GetShow(id)
		if err != nil {
			continue
		}
		for _, se := range sib.Seasons {
			for _, sibEp := range se.Episodes {
				if sibEp.FilePath == "" {
					continue
				}
				ours := findEpisode(sh, sibEp.Season, sibEp.Episode)
				if ours == nil || ours.FilePath != "" {
					continue
				}
				p := parser.Result{Kind: "episode"}
				p.Ep.Season, p.Ep.Episode, p.Ep.EpisodeEnd = sibEp.Season, sibEp.Episode, sibEp.Episode
				if _, err := s.linkShowFile(sh, p, sibEp.FilePath, sibEp.Quality, sibEp.Source, filepath.Base(sibEp.FilePath)); err != nil {
					log.Printf("reely: link %q S%02dE%02d from sibling: %v", sh.Title, sibEp.Season, sibEp.Episode, err)
					continue
				}
				ours.FilePath = sibEp.FilePath // keep the in-memory tree honest for this pass
				linked++
			}
		}
	}
	return linked
}
