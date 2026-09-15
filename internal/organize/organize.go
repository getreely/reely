// Package organize brings a library's files into line with its naming
// templates. It is the answer to a library that grew across a rename, a
// template change, or another tool's idea of what a folder should be
// called: reely knows where every file it tracks ought to live, so it can
// put them there rather than leaving it to a human with a file manager.
//
// Nothing moves without being planned first. Plan reports every move it
// would make and Apply performs exactly that list, so the destructive step
// is always something a person has already read.
package organize

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/naming"
)

// Move is one file that is not where its template says it should be.
type Move struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Title is what the move is about, for a human reading the list
	Title string `json:"title"`
	Kind  string `json:"kind"` // movie | episode
}

// Plan is what Apply would do, and what the UI shows first.
type Plan struct {
	Moves []Move `json:"moves"`
	// Emptied are directories the moves would leave with nothing in them
	Emptied []string `json:"emptied"`
	// Leftover are directories losing files that will NOT empty, because
	// something reely did not put there is still in them. Named rather
	// than silently skipped: with hundreds of folders, "which ones do I
	// still have to look at" is the whole question, and hunting for them
	// by hand afterwards is the tedium this is meant to end.
	Leftover []string `json:"leftover"`
}

// Result is what Apply actually did.
type Result struct {
	Moved   int      `json:"moved"`
	Removed []string `json:"removed"` // empty directories cleaned up
	// Kept are the folders that survived, and why they did — a stray
	// file nobody claimed. reely never deletes one it did not place.
	Kept   []string `json:"kept"`
	Errors []string `json:"errors"`
}

// SettingsReader is the sliver of settings the templates come from.
type SettingsReader interface {
	Get(key string) string
}

type Service struct {
	Catalog  *catalog.Store
	Settings SettingsReader
}

func (s *Service) template(key, fallback string) string {
	if s.Settings != nil {
		if v := s.Settings.Get(key); v != "" {
			return v
		}
	}
	return fallback
}

const (
	defaultMovieTemplate = naming.DefaultMovieTemplate
	defaultShowTemplate  = naming.DefaultShowTemplate
)

// Plan works out every move a library needs. libraryID 0 plans all of them.
func (s *Service) Plan(libraryID int64) (*Plan, error) {
	files, err := s.Catalog.OrganizeFiles(libraryID)
	if err != nil {
		return nil, err
	}
	return s.planFor(files), nil
}

// PlanTitle is the same plan for ONE title. Correcting a single entry
// should move that entry's files and nothing else — an owner fixing one
// film has not asked for the rest of the library to be reorganised, and
// making them accept that is how a small fix stops being worth doing.
func (s *Service) PlanTitle(kind string, id int64) (*Plan, error) {
	files, err := s.Catalog.OrganizeFilesFor(kind, id)
	if err != nil {
		return nil, err
	}
	return s.planFor(files), nil
}

// Destination is where one file belongs under the current templates.
// Exported so a caller can ask whether a file is already where reely
// would have put it — which is how it tells a library it organises from
// one somebody else lays out by hand.
func (s *Service) Destination(f catalog.OrganizeFile) string { return s.destination(f) }

// Placed reports whether a file is where reely would have put it.
//
// It accepts the path the template gives NOW, and also the one it would
// have given before the template named any ids. A library organised
// under "{Title} ({Year})" did not stop being reely's the day the
// default started naming a TMDB id — and treating it as somebody's
// hand-made layout would quietly disable every single-title rename on
// exactly the libraries that have been organised the longest.
func (s *Service) Placed(f catalog.OrganizeFile) bool {
	if dest := s.destination(f); dest != "" && dest == f.Path {
		return true
	}
	bare := f
	bare.TmdbID, bare.TvdbID, bare.ImdbID = 0, 0, ""
	dest := s.destination(bare)
	return dest != "" && dest == f.Path
}

func (s *Service) planFor(files []catalog.OrganizeFile) *Plan {
	// a multi-episode file backs several rows and must move once; the
	// lowest-numbered episode names it, matching how it was filed
	byPath := map[string]catalog.OrganizeFile{}
	for _, f := range files {
		cur, seen := byPath[f.Path]
		if !seen || f.Kind == "movie" ||
			f.Season < cur.Season || (f.Season == cur.Season && f.Episode < cur.Episode) {
			byPath[f.Path] = f
		}
	}

	plan := &Plan{Moves: []Move{}, Emptied: []string{}, Leftover: []string{}}
	losing := map[string]int{}   // directory → files leaving it
	videosIn := map[string]int{} // directory → videos moving out of it
	roots := map[string]bool{}   // the library folders, never prunable
	var carry []carried          // the moves whose sidecars follow them
	for _, f := range byPath {
		roots[filepath.Clean(f.LibraryPath)] = true
	}
	for path, f := range byPath {
		dest := s.destination(f)
		if dest == "" || dest == path {
			continue
		}
		// a plan is a promise about what will happen, so a row whose file is
		// not on disk — a stale path, a library moved out from under reely —
		// is left out rather than listed as a move that is certain to fail
		if _, err := os.Stat(path); err != nil {
			continue
		}
		plan.Moves = append(plan.Moves, Move{
			From: path, To: dest, Title: label(f), Kind: f.Kind,
		})
		// a rename inside the same folder leaves it exactly as full as it
		// was, so only a move that actually departs counts against it
		if from, to := filepath.Dir(path), filepath.Dir(dest); from != to {
			losing[from]++
		}
		videosIn[filepath.Dir(path)]++
		carry = append(carry, carried{f: f, path: path, dest: dest})
	}
	// Sidecars travel with the file they belong to. A subtitle left
	// behind stops being a subtitle — Plex reads those straight off disk
	// beside the video — and every file left behind is also a folder that
	// cannot be pruned, which is the same complaint from the other end.
	for _, c := range carry {
		for _, m := range s.sidecarsFor(c, roots, videosIn) {
			plan.Moves = append(plan.Moves, m)
			if from, to := filepath.Dir(m.From), filepath.Dir(m.To); from != to {
				losing[from]++
			}
		}
	}
	sort.Slice(plan.Moves, func(i, j int) bool {
		if plan.Moves[i].Title != plan.Moves[j].Title {
			return plan.Moves[i].Title < plan.Moves[j].Title
		}
		return plan.Moves[i].From < plan.Moves[j].From
	})

	// a directory is only reported as emptied if every file in it is
	// leaving, and only if it is a folder INSIDE a library rather than
	// the library itself
	for dir, leaving := range losing {
		if !insideALibrary(dir, roots) {
			continue
		}
		n, err := countFiles(dir)
		if err != nil {
			continue
		}
		if n == leaving {
			plan.Emptied = append(plan.Emptied, dir)
		} else {
			plan.Leftover = append(plan.Leftover, dir)
		}
	}
	sort.Strings(plan.Emptied)
	sort.Strings(plan.Leftover)
	return plan
}

// destination is where one file belongs, absolute, with its extension kept.
func (s *Service) destination(f catalog.OrganizeFile) string {
	if f.LibraryPath == "" {
		return ""
	}
	var rel string
	if f.Kind == "movie" {
		rel = naming.Movie(s.template("naming_movie", defaultMovieTemplate), naming.MovieFields{
			Title: f.Title, Year: f.Year, Quality: f.Quality, Source: f.Source,
			TmdbID: f.TmdbID, ImdbID: f.ImdbID,
		})
	} else {
		rel = naming.Episode(s.template("naming_show", defaultShowTemplate), naming.EpisodeFields{
			Title: f.Title, Year: f.Year, Season: f.Season, Episode: f.Episode,
			EpisodeTitle: f.EpisodeTitle, Quality: f.Quality, Source: f.Source,
			TmdbID: f.TmdbID, TvdbID: f.TvdbID, ImdbID: f.ImdbID,
		})
	}
	if rel == "" {
		return ""
	}
	return filepath.Join(f.LibraryPath, rel+strings.ToLower(filepath.Ext(f.Path)))
}

func label(f catalog.OrganizeFile) string {
	if f.Kind == "movie" {
		return f.Title
	}
	return fmt.Sprintf("%s S%02dE%02d", f.Title, f.Season, f.Episode)
}

// Apply performs a plan. Each move is independent: one failure is recorded
// and the rest still run, because a single unreadable file should not
// strand a library halfway organized.
func (s *Service) Apply(plan *Plan) *Result {
	res := &Result{Removed: []string{}, Kept: []string{}, Errors: []string{}}
	for _, m := range plan.Moves {
		if err := s.move(m); err != nil {
			log.Printf("reely: organize %q: %v", m.From, err)
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", m.Title, err))
			continue
		}
		res.Moved++
	}
	// only prune directories the plan predicted would empty, and only if
	// they really are empty now
	for _, dir := range plan.Emptied {
		if n, err := countFiles(dir); err != nil || n != 0 {
			continue
		}
		if err := removeEmptyTree(dir); err != nil {
			// worth saying: the plan predicted this folder would go and it
			// did not, which is the kind of thing that otherwise just
			// accumulates unexplained
			log.Printf("reely: organize: %q was left behind: %v", dir, err)
			continue
		}
		res.Removed = append(res.Removed, dir)
	}
	// what is still standing, so nobody has to go looking. A folder here
	// held something reely did not place; it is named rather than
	// quietly left, because with hundreds of them "which ones still need
	// me" is the only question worth answering.
	for _, dir := range plan.Leftover {
		if n, err := countFiles(dir); err == nil && n > 0 {
			res.Kept = append(res.Kept, dir)
		}
	}
	return res
}

func (s *Service) move(m Move) error {
	if m.From == m.To {
		return nil
	}
	if _, err := os.Stat(m.From); err != nil {
		return fmt.Errorf("gone from disk: %w", err)
	}
	// never write over something already there — a collision means two rows
	// claim one name, which is a question for a person, not a silent
	// overwrite of somebody's file
	if _, err := os.Stat(m.To); err == nil {
		return errors.New("something is already at the destination")
	}
	if err := os.MkdirAll(filepath.Dir(m.To), 0o755); err != nil {
		return err
	}
	if err := os.Rename(m.From, m.To); err != nil {
		// a library spanning volumes cannot rename across them
		if err := copyFile(m.From, m.To); err != nil {
			return err
		}
		_ = os.Remove(m.From)
	}
	if _, err := s.Catalog.RepathFile(m.From, m.To); err != nil {
		return fmt.Errorf("moved, but the catalog still points at the old path: %w", err)
	}
	return nil
}

// carried is one video move, kept so its sidecars can be planned once
// every video's destination is known.
type carried struct {
	f    catalog.OrganizeFile
	path string
	dest string
}

// artNames are the filenames a media server reads as a title's own
// artwork and metadata. They do not carry the title in them, so they can
// only be claimed where the folder unambiguously belongs to one film.
var artNames = map[string]bool{
	"poster.jpg": true, "poster.png": true,
	"fanart.jpg": true, "fanart.png": true,
	"banner.jpg": true, "banner.png": true,
	"clearlogo.png": true, "logo.png": true,
	"thumb.jpg": true, "disc.png": true,
	"movie.nfo": true,
}

// sidecarsFor is the files that belong to one video and should travel
// with it.
//
// Two kinds, and the difference is how certain the claim is.
//
// A file sharing the video's name is its own beyond doubt:
// "Film (1999).en.srt" next to "Film (1999).mkv" cannot belong to
// anything else, so it follows for movies and episodes alike, keeping
// whatever it carries after the stem — the language, the "forced".
//
// Artwork named by convention — poster.jpg, fanart.jpg — names no title
// at all, so it is only claimed where the folder can only be about one
// film: a MOVIE, the single video leaving that folder, and not the
// library root, where a flat library would hand one film everything.
//
// Everything else stays. A sample, a release group's .txt, a .DS_Store
// some Mac left while browsing: reely did not put them there and does
// not move or delete them. The folder survives, and says so.
func (s *Service) sidecarsFor(c carried, roots map[string]bool, videosIn map[string]int) []Move {
	dir := filepath.Dir(c.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	video := filepath.Base(c.path)
	stem := strings.TrimSuffix(video, filepath.Ext(video))
	newStem := strings.TrimSuffix(c.dest, filepath.Ext(c.dest))
	soleFilm := c.f.Kind == "movie" && videosIn[dir] == 1 && insideALibrary(dir, roots)

	out := []Move{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == video {
			continue
		}
		switch {
		case len(name) > len(stem) && strings.HasPrefix(name, stem) &&
			(name[len(stem)] == '.' || name[len(stem)] == '-'):
			out = append(out, Move{
				From: filepath.Join(dir, name), To: newStem + name[len(stem):],
				Title: c.f.Title, Kind: c.f.Kind,
			})
		case soleFilm && artNames[strings.ToLower(name)]:
			out = append(out, Move{
				From: filepath.Join(dir, name), To: filepath.Join(filepath.Dir(c.dest), name),
				Title: c.f.Title, Kind: c.f.Kind,
			})
		}
	}
	return out
}

// insideALibrary reports whether dir sits strictly below a library root.
//
// A flat library — files directly in the library folder, which is
// exactly the shape somebody runs organize to fix — has every file
// leaving the root at once. The root then looks emptied like any other
// folder, and removing it would delete the library itself: reely's own
// configured path, gone, because it tidied it too well. Nothing had
// stopped that; the bare os.Remove simply failed often enough to hide
// it, and would have succeeded on a root with nothing else in it.
func insideALibrary(dir string, roots map[string]bool) bool {
	clean := filepath.Clean(dir)
	inside := false
	for root := range roots {
		if root == "" || root == "." || root == string(filepath.Separator) {
			continue
		}
		if clean == root {
			return false
		}
		if strings.HasPrefix(clean, root+string(filepath.Separator)) {
			inside = true
		}
	}
	return inside
}

// removeEmptyTree deletes dir along with any directories inside it,
// provided none of them holds a file.
//
// os.Remove alone was not enough. countFiles counts FILES at any depth,
// so a folder holding nothing but empty subdirectories — a Subs/ or
// Featurettes/ another tool made, a season folder whose episodes have
// all left — counts zero and is predicted to empty. os.Remove then
// refuses it for still having directories in it, and the folder stayed.
//
// Bottom-up, because a directory cannot go until the ones inside it
// have. It never deletes a file: the caller has already established
// there are none, and meeting one here stops the walk rather than
// removing it.
func removeEmptyTree(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			return fmt.Errorf("%s is not empty after all", e.Name())
		}
		if err := removeEmptyTree(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(dir)
}

// countFiles counts the files (at any depth) under dir.
func countFiles(dir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
