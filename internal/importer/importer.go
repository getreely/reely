// Package importer scans a library's folder, matches each media file to a
// TMDB title through the parser, and stores the result. It is the bridge
// between files on disk and the catalog — the same stage a completed
// download will flow through once the grab loop exists.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/mediainfo"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/quality"
)

// videoExts are the container extensions Reely treats as media files.
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	".mov": true, ".wmv": true, ".mpg": true, ".mpeg": true, ".ts": true,
}

type Importer struct {
	cat  *catalog.Store
	tmdb *metadata.TMDB
	// tvdb is optional — show libraries match through it when configured,
	// the same routing every other add path follows
	tvdb *metadata.TVDB
}

func New(cat *catalog.Store, tmdb *metadata.TMDB, tvdb *metadata.TVDB) *Importer {
	return &Importer{cat: cat, tmdb: tmdb, tvdb: tvdb}
}

func (imp *Importer) tvdbEnabled() bool { return imp.tvdb != nil && imp.tvdb.Configured() }

// Result summarizes one scan for the caller and the activity log.
type Result struct {
	Files    int      `json:"files"`
	Matched  int      `json:"matched"`
	Imported int      `json:"imported"` // files attached to a title
	Skipped  int      `json:"skipped"`
	Missing  int      `json:"missing"` // attachments cleared — their files are gone
	Warnings []string `json:"warnings"`
}

// ScanLibrary walks a library's path, parses every media file, matches it to
// TMDB, and attaches it. Movie libraries import movies; show libraries import
// episodes. Matching is cached per (title, year) within a scan so a season of
// one show costs a single lookup, not one per episode.
func (imp *Importer) ScanLibrary(ctx context.Context, lib *catalog.Library) (*Result, error) {
	if !imp.tmdb.Configured() {
		return nil, fmt.Errorf("add a TMDB API key in Settings before scanning")
	}
	res := &Result{}
	// (title|year) → local show/movie id, so repeated titles resolve once
	matched := map[string]int64{}

	files, err := mediaFiles(lib.Path)
	if err != nil {
		return nil, err
	}
	res.Files = len(files)
	log.Printf("reely: scanning %s — %d files", lib.Name, len(files))

	for i, path := range files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		// a big library scans in silence for minutes otherwise — a heartbeat
		// every thousand files says it is alive, and how far along
		if i > 0 && i%1000 == 0 {
			log.Printf("reely: scanning %s — %d/%d files", lib.Name, i, len(files))
		}
		p := parser.Parse(filepath.Base(path))
		applyFolderIdentity(lib.Path, path, &p)
		resolveScannedQuality(path, &p)
		info, statErr := os.Stat(path)
		var size int64
		if statErr == nil {
			size = info.Size()
		}

		if lib.Kind == "movies" {
			if p.Kind != "movie" {
				res.Skipped++
				continue
			}
			if err := imp.importMovie(ctx, lib, path, size, p, matched, res); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", filepath.Base(path), err))
			}
			continue
		}

		// show library
		if p.Kind != "episode" {
			res.Skipped++
			continue
		}
		if err := imp.importEpisode(ctx, lib, path, size, p, matched, res); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", filepath.Base(path), err))
		}
	}
	imp.clearMissing(lib, res)
	log.Printf("reely: scanned %s — %d files, %d imported, %d skipped, %d gone, %d warnings",
		lib.Name, res.Files, res.Imported, res.Skipped, res.Missing, len(res.Warnings))
	imp.recordScan(lib, res)
	return res, nil
}

// clearMissing is the scan's other half: rows still claiming a file whose
// file is gone stop reading "on disk", so a deletion made behind reely's
// back turns the title wanted again instead of haunting it.
//
// Only a confirmed absence clears a row. If the library root itself is
// unreachable — an unmounted share stats exactly like a deleted file —
// nothing is touched: wiping every attachment over a flaky mount would
// re-download the whole library.
func (imp *Importer) clearMissing(lib *catalog.Library, res *Result) {
	if _, err := os.Stat(lib.Path); err != nil {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("library folder unreachable — file checks skipped: %v", err))
		return
	}
	attached, err := imp.cat.AttachedMovieFiles(lib.ID)
	clear := imp.cat.ClearMovieFile
	if lib.Kind == "shows" {
		attached, err = imp.cat.AttachedEpisodeFiles(lib.ID)
		clear = imp.cat.ClearEpisodeFile
	}
	if err != nil {
		log.Printf("reely: %s: listing attached files: %v", lib.Name, err)
		return
	}
	for _, f := range attached {
		if _, err := os.Stat(f.Path); !os.IsNotExist(err) {
			continue // present, or unknowable — either way not provably gone
		}
		if err := clear(f.ID); err != nil {
			log.Printf("reely: %s: clearing missing file: %v", lib.Name, err)
			continue
		}
		res.Missing++
	}
}

// recordScan writes the scan's outcome into history so the Activity page
// can answer "what happened to my files" — warnings capped so a wildly
// messy folder doesn't balloon the row.
func (imp *Importer) recordScan(lib *catalog.Library, res *Result) {
	warnings := res.Warnings
	if len(warnings) > 20 {
		warnings = append(warnings[:20:20], fmt.Sprintf("… and %d more", len(res.Warnings)-20))
	}
	title := fmt.Sprintf("%s — %d files, %d imported, %d skipped",
		lib.Name, res.Files, res.Imported, res.Skipped)
	if res.Missing > 0 {
		title += fmt.Sprintf(", %d gone from disk", res.Missing)
	}
	detail, _ := json.Marshal(map[string]any{
		"title":    title,
		"warnings": warnings,
	})
	if err := imp.cat.AddHistory("scanned", 0, 0, 0, string(detail)); err != nil {
		log.Printf("reely: record scan: %v", err)
	}
}

func (imp *Importer) importMovie(ctx context.Context, lib *catalog.Library, path string, size int64,
	p parser.Result, matched map[string]int64, res *Result) error {
	key := matchKey(p.Title, p.Year)
	movieID, ok := matched[key]
	if !ok {
		hits, err := imp.tmdb.SearchMovies(ctx, p.Title, p.Year)
		if err != nil {
			return err
		}
		best, nearest := bestMatch(hits, p.Title, p.Year)
		if best == nil {
			res.Skipped++
			if nearest != "" {
				return fmt.Errorf("no confident TMDB match for %q — closest was %q", p.Title, nearest)
			}
			return fmt.Errorf("no TMDB match for %q", p.Title)
		}
		detail, err := imp.tmdb.Movie(ctx, best.TmdbID)
		if err != nil {
			return err
		}
		id, err := imp.cat.UpsertMovie(detail, lib.ID)
		if err != nil {
			return err
		}
		movieID = id
		matched[key] = id
		res.Matched++
	}
	if err := imp.cat.AttachScannedMovieFile(movieID, path, size, p.Quality, p.Source); err != nil {
		return err
	}
	res.Imported++
	return nil
}

func (imp *Importer) importEpisode(ctx context.Context, lib *catalog.Library, path string, size int64,
	p parser.Result, matched map[string]int64, res *Result) error {
	key := matchKey(p.Title, p.Year)
	showID, ok := matched[key]
	if !ok {
		id, err := imp.matchShow(ctx, lib, p)
		if err != nil {
			if errors.Is(err, errNoMatch) {
				res.Skipped++
			}
			return err
		}
		showID = id
		matched[key] = id
		res.Matched++
	}
	// a multi-episode file (S01E02E03) attaches to every episode it spans
	for ep := p.Ep.Episode; ep <= p.Ep.EpisodeEnd; ep++ {
		epID, season, err := imp.episodeForScan(showID, p.Ep.Season, ep)
		if err != nil {
			return err
		}
		if epID == 0 {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s: TMDB has no S%02dE%02d", filepath.Base(path), season, ep))
			continue
		}
		if err := imp.cat.AttachScannedEpisodeFile(epID, path, size, p.Quality, p.Source); err != nil {
			return err
		}
	}
	res.Imported++
	return nil
}

// episodeForScan resolves a scanned file's SxxEyy to an episode row. The
// literal numbers win; when they miss and the show carries a release
// numbering offset (a TMDB-split revival), the file may be named in scene
// numbering — S08E01 for the show's own S01E01 — so the mapped season is
// tried second. Returns the season actually used, for warnings.
func (imp *Importer) episodeForScan(showID int64, season, episode int) (int64, int, error) {
	epID, err := imp.cat.EpisodeID(showID, season, episode)
	if err != nil || epID != 0 {
		return epID, season, err
	}
	offset, err := imp.cat.ShowSeasonOffset(showID)
	if err != nil || offset == 0 {
		return 0, season, err
	}
	epID, err = imp.cat.EpisodeID(showID, season-offset, episode)
	if err != nil || epID == 0 {
		return 0, season, err
	}
	return epID, season - offset, nil
}

var errNoMatch = errors.New("no metadata match")

// matchShow resolves a parsed show name to a stored show through the
// configured source: TheTVDB when a key is present (one continuing series
// per show, native numbering), TMDB otherwise.
func (imp *Importer) matchShow(ctx context.Context, lib *catalog.Library, p parser.Result) (int64, error) {
	if imp.tvdbEnabled() {
		hits, err := imp.tvdb.SearchShows(ctx, p.Title)
		if err != nil {
			return 0, err
		}
		best, nearest := bestMatch(hits, p.Title, p.Year)
		if best == nil {
			if nearest != "" {
				return 0, fmt.Errorf("no confident TVDB %w for %q — closest was %q", errNoMatch, p.Title, nearest)
			}
			return 0, fmt.Errorf("no TVDB %w for %q", errNoMatch, p.Title)
		}
		detail, err := imp.tvdb.Show(ctx, best.TvdbID)
		if err != nil {
			return 0, err
		}
		metadata.EnrichShowFromTMDB(ctx, imp.tmdb, detail)
		return imp.cat.UpsertShowTVDB(detail, lib.ID)
	}
	hits, err := imp.tmdb.SearchShows(ctx, p.Title, p.Year)
	if err != nil {
		return 0, err
	}
	best, nearest := bestMatch(hits, p.Title, p.Year)
	if best == nil {
		if nearest != "" {
			return 0, fmt.Errorf("no confident TMDB %w for %q — closest was %q", errNoMatch, p.Title, nearest)
		}
		return 0, fmt.Errorf("no TMDB %w for %q", errNoMatch, p.Title)
	}
	detail, err := imp.tmdb.Show(ctx, best.TmdbID)
	if err != nil {
		return 0, err
	}
	return imp.cat.UpsertShow(detail, lib.ID)
}

// mediaFiles returns every video file under root, sorted for a stable scan
// order (so a show's episodes import together and logs read predictably).
func mediaFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable dir — skip, don't abort the whole scan
		}
		if d.IsDir() {
			return nil
		}
		if videoExts[strings.ToLower(filepath.Ext(path))] {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// applyFolderIdentity lets the file's folder name the title. Filenames
// routinely drop the year ("Wife Swap - S01E01 - …"), and a year-less
// title collapses shows that share a name — the 2019 reboot's files were
// getting matched to the 2004 original. The organize step itself writes
// "Title (Year)" folders precisely to keep those apart, so the deepest
// such folder between the library root and the file is the closest thing
// the disk has to an authoritative identity.
//
// The rules stay deliberately narrow: a folder with a year is adopted
// outright; a year-less folder only fills in when the filename carried no
// title at all ("S01E01.mkv"). A grouping folder ("Reality", "Marvel
// Collection") therefore never overrides a real title parsed from the
// filename, and a flat layout changes nothing.
func applyFolderIdentity(root, path string, p *parser.Result) {
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	var title string
	var year int
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		f := parser.Parse(part)
		// season folders ("Season 01") and unparseable names carry no title
		if f.Kind != "movie" || f.Title == "" {
			continue
		}
		// deepest qualifying folder wins; one with a year beats one without
		if f.Year > 0 || title == "" || year == 0 {
			title, year = f.Title, f.Year
		}
	}
	switch {
	case title != "" && year > 0:
		p.Title, p.Year = title, year
	case title != "" && p.Title == "":
		p.Title = title
	}
}

func matchKey(title string, year int) string {
	return fmt.Sprintf("%s|%d", strings.ToLower(strings.TrimSpace(title)), year)
}

// stripCountryParen drops a trailing "(us)"-style region marker from an
// already-lowercased title.
func stripCountryParen(s string) string {
	if i := strings.LastIndex(s, " ("); i > 0 && strings.HasSuffix(s, ")") {
		switch s[i+2 : len(s)-1] {
		case "us", "uk", "gb", "au", "nz", "ca":
			return s[:i]
		}
	}
	return s
}

// normalizeTitle reduces a title to comparable letters and digits, so
// punctuation and casing can't decide a match: "Marvel's Daredevil" and
// "marvels daredevil" are the same name.
func normalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// bestMatch picks the search result for a parsed name, or nothing. An
// exact title wins — with the year when both sides know one, without it
// otherwise (the year is a tiebreak between same-named shows, never a
// requirement: the databases omit it often enough that demanding it
// would strand real matches). Failing that, a candidate whose title is
// contained in the parsed name is accepted: that is how decoration
// resolves, "Marvel's Daredevil" to "Daredevil".
//
// Nothing else is accepted, and that restraint is the point. Falling back
// to the provider's top hit is how a folder called "Monster" became
// Monster High: a short query prefix-matches a longer, more popular
// franchise, search ranks by relevance rather than by exactness, and the
// wrong show is then recreated by every later scan no matter how many
// times it is deleted. The direction matters — a candidate SHORTER than
// the query is the query carrying extra decoration, while a candidate
// LONGER is a different title that merely starts the same way.
//
// TVDB disambiguates same-named shows with a country suffix — "Kitchen
// Nightmares (US)" — which file names rarely carry; the suffix folds away
// BEFORE the direction rule, or every such title would read as "a longer
// name that merely starts the same" and be refused.
//
// An unmatched file is a warning someone can act on; a confidently wrong
// one silently poisons the library. Returns the nearest rejected title so
// the warning can name what it turned down.
func bestMatch(hits []metadata.SearchResult, title string, year int) (*metadata.SearchResult, string) {
	if len(hits) == 0 {
		return nil, ""
	}
	want := normalizeTitle(title)
	var titleHit, contained *metadata.SearchResult
	for i := range hits {
		h := &hits[i]
		got := normalizeTitle(stripCountryParen(strings.ToLower(strings.TrimSpace(h.Title))))
		if got == want {
			if year == 0 || h.Year == 0 || h.Year == year {
				return h, "" // exact name, and the year agrees or is unknown
			}
			// exact name, disagreeing year: the fallback when no candidate
			// carries the right year — how a revival's folder ("Kitchen
			// Nightmares (2023)") still finds the one continuing series
			if titleHit == nil {
				titleHit = h
			}
			continue
		}
		// the query carries decoration the candidate's title lacks; a year
		// that disagrees outright still vetoes it
		if got != "" && strings.Contains(want, got) && contained == nil {
			if year == 0 || h.Year == 0 || absInt(h.Year-year) <= 1 {
				contained = h
			}
		}
	}
	if titleHit != nil {
		return titleHit, ""
	}
	if contained != nil {
		return contained, ""
	}
	return nil, hits[0].Title
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// resolveScannedQuality settles what quality a scan records for one file.
// A name that carries no resolution is not a dead end: the container
// knows its own pixel size. An inferred SD quality gets the probe too —
// convention is good enough to judge a release, but a file in hand can
// simply be measured, and a mislabeled HD file must not be recorded as
// 480p on convention. When the probe can't read the container, the
// inference stands: an honest convention beats a blank. Source is never
// touched — nothing in a container says disc or stream.
func resolveScannedQuality(path string, p *parser.Result) {
	if p.Quality != "" && !p.QualityInferred {
		return // a real marker in the name is already the truth
	}
	d, err := mediainfo.Probe(path)
	if err != nil {
		return
	}
	if q := quality.FromDimensions(d.Width, d.Height); q != "" {
		p.Quality = q
		p.QualityInferred = false
	}
}
