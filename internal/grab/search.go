// Package grab is the acquisition loop's brain: it asks the indexers what
// exists, judges every answer against the wanted title's quality profile,
// and (in a later piece) sends the winner to the download client.
package grab

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/prowlarr"
	"github.com/getreely/reely/internal/quality"
)

// ErrUnknownTarget marks a search aimed at an episode or season the show
// doesn't carry — a caller mistake, not an indexer failure.
var ErrUnknownTarget = errors.New("unknown target")

// Searcher is the indexer surface the service needs — prowlarr.Client in
// production, a stub in tests.
type Searcher interface {
	Search(ctx context.Context, query string, categories []int) ([]prowlarr.Release, error)
	SearchIDs(ctx context.Context, searchType, query string, categories []int) ([]prowlarr.Release, error)
	SearchPage(ctx context.Context, query string, categories []int, offset, limit int) ([]prowlarr.Release, error)
	Configured() bool
}

type Service struct {
	Indexer Searcher
	// One client per protocol. Both optional and most installs run one:
	// what a release can be grabbed with is a fact about which of these
	// is configured, not a setting to be argued with.
	Usenet   Downloader // SABnzbd
	Torrent  Downloader // qBittorrent
	Catalog  *catalog.Store
	Settings SettingsReader // naming templates; nil falls back to defaults
	// Placed is called once per import sweep with the folders files
	// landed in, so somebody who can talk to the media server can ask it
	// to look now rather than at its own leisure. Optional: nil is an
	// install with no media server, which is most of the tests.
	Placed func(dirs []string)

	mu       sync.Mutex
	problems map[string]*importProblem // nzo id → why its import is stuck
	// nzo ids already filed, so a job whose SAB history entry outlives its
	// import is never imported twice nor mistaken for a failure
	importedJobs map[string]time.Time
	// folders files landed in during the sweep now running, or nil
	// between sweeps. Guarded by mu like everything else here.
	landedIn    map[string]bool
	queue       []searchTarget                  // active searches waiting their paced turn
	queued      map[searchTarget]bool           // dedupe for the queue
	origins     map[searchTarget]string         // who asked for each queued search — the history trail's "via"
	scheduled   map[searchTarget]time.Time      // release-day pass: last scheduled search per target
	downloading *downloadingCache               // titles with a SAB job in flight, briefly cached
	rssSeen     map[string]map[string]rssCursor // feed → indexer → where the last RSS sync left off
}

// ReleaseView is one judged search result: what the indexer reported plus
// what the scorer made of it. Rejected rows keep their reason — an
// interactive search that hides why is useless for tuning profiles.
type ReleaseView struct {
	Title       string   `json:"title"`
	Indexer     string   `json:"indexer"`
	Protocol    string   `json:"protocol"`
	PublishDate string   `json:"publishDate"`
	DownloadURL string   `json:"downloadUrl"` // echoed back on grab
	Size        int64    `json:"size"`
	Quality     string   `json:"quality"`
	Source      string   `json:"source"`
	Languages   []string `json:"languages,omitempty"` // parsed audio tags: Multi, French, …
	HDR         bool     `json:"hdr"`
	Proper      bool     `json:"proper"`
	Accepted    bool     `json:"accepted"`
	Score       int      `json:"score"`
	FormatScore int      `json:"formatScore"`
	Reason      string   `json:"reason,omitempty"`
	Terms       []string `json:"terms,omitempty"` // matched terms + custom formats
}

// searchBoth runs the free-text queries and, when an id query is given,
// the id-keyed one too, merging the answers. Ids are how Sonarr and
// Radarr ask — precise for the indexers whose caps support them, and
// immune to two shows sharing a title — but not every indexer answers
// them, so the text queries always run and no result set is allowed to
// hide another. The first text query is the canonical ask and its
// failure fails the search; extra queries (an anthology season's
// subtitle form) are additional ears, logged and skipped on error.
// Duplicates (several queries finding the same posting) collapse by
// identity.
func (s *Service) searchBoth(ctx context.Context, searchType, idQuery string, cats []int, textQueries ...string) ([]prowlarr.Release, error) {
	var releases []prowlarr.Release
	seen := map[string]bool{}
	add := func(rs []prowlarr.Release) {
		for _, rel := range rs {
			if !seen[releaseID(rel)] {
				seen[releaseID(rel)] = true
				releases = append(releases, rel)
			}
		}
	}
	for i, q := range textQueries {
		rs, err := s.Indexer.Search(ctx, q, cats)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			log.Printf("reely: search %q: %v", q, err)
			continue
		}
		add(rs)
	}
	if idQuery != "" {
		if rs, err := s.Indexer.SearchIDs(ctx, searchType, idQuery, cats); err != nil {
			log.Printf("reely: id search %q: %v", idQuery, err)
		} else {
			add(rs)
		}
	}
	return releases, nil
}

// MovieReleases runs an interactive search for one movie.
func (s *Service) MovieReleases(ctx context.Context, m *catalog.MovieDetails) ([]ReleaseView, error) {
	profile, err := s.resolveProfile(m.ProfileID, m.LibraryID, "movies")
	if err != nil {
		return nil, err
	}
	query := m.Title
	if m.Year > 0 {
		query = fmt.Sprintf("%s %d", m.Title, m.Year)
	}
	// both ids, the way Radarr asks: indexers answer whichever their caps
	// support, and the TMDB id is always known even when IMDb's isn't
	idQuery := fmt.Sprintf("{tmdbid:%d}", m.TmdbID)
	if m.ImdbID != "" {
		idQuery = fmt.Sprintf("{imdbid:%s}", m.ImdbID) + idQuery
	}
	releases, err := s.searchBoth(ctx, "movie", idQuery, prowlarr.MovieCats, query)
	if err != nil {
		return nil, err
	}
	current, currentSrc := "", ""
	if m.FilePath != "" {
		current, currentSrc = m.Quality, m.Source
	}
	wantFor := func(parser.Result) quality.Want {
		return quality.Want{
			RuntimeMin: m.Runtime, HasFile: m.FilePath != "",
			CurrentQuality: current, CurrentSource: currentSrc,
		}
	}
	today := time.Now().UTC().Format("2006-01-02")
	return judge(releases, s.protocols(), profile, wantFor, func(rel prowlarr.Release, p parser.Result) string {
		if p.Kind != "movie" {
			return "not a movie release"
		}
		// identity: ids first, the way Radarr maps — a reported id that
		// agrees IS the movie, whatever the release chose to call it (a
		// foreign cut, a festival title); the name heuristics only run
		// without one
		if !movieIDAgrees(rel, m.TmdbID, m.ImdbID) {
			if !titleMatchMovie(p.Title, &m.Movie) {
				return "title does not match"
			}
			if p.Year > 0 && m.Year > 0 && !yearIsTitle(p.Year, m.Title) && absInt(p.Year-m.Year) > 1 {
				return fmt.Sprintf("wrong year (%d)", p.Year)
			}
			if reason := movieIDMismatch(rel, m.TmdbID, m.ImdbID); reason != "" {
				return reason
			}
		}
		// timing: the availability gate exempts nothing, agreeing id
		// included — a movie's id mapping comes from the release's own
		// NFO, which a faker writes, so an id can say which movie a
		// release CLAIMS to be but never that the movie is actually out
		if !movieAvailable(&m.Movie, today) {
			return "not released yet — likely mislabeled or fake (grab by hand if it's real)"
		}
		return ""
	}), nil
}

// EpisodeReleases searches for one episode of a show.
func (s *Service) EpisodeReleases(ctx context.Context, sh *catalog.ShowDetails, season, episode int) ([]ReleaseView, error) {
	profile, err := s.resolveProfile(sh.ProfileID, sh.LibraryID, "shows")
	if err != nil {
		return nil, err
	}
	ep := findEpisode(sh, season, episode)
	if ep == nil {
		return nil, fmt.Errorf("%w: no S%02dE%02d on this show", ErrUnknownTarget, season, episode)
	}
	// searches speak the numbering releases use — a show TMDB split off a
	// revival (S1 here, S8 to the scene) is asked for by its scene number,
	// and so is one TheXEM maps episode by episode (TVDB's S09E08 of
	// Kitchen Nightmares releases as S10E08)
	relSeason, relEpisode := releaseNumbering(sh, season, episode)
	queries := []string{fmt.Sprintf("%s S%02dE%02d", queryTitle(sh.Title), relSeason, relEpisode)}
	// a subtitled season's releases name themselves after the story and
	// number themselves S01 — ask in that form too
	if sub, ok := seasonSubtitleQuery(sh, season); ok {
		queries = append(queries, fmt.Sprintf("%s S01E%02d", sub, episode))
	}
	idQuery := ""
	if sh.TvdbID > 0 {
		idQuery = fmt.Sprintf("{tvdbid:%d}{season:%d}{episode:%d}", sh.TvdbID, relSeason, relEpisode)
	}
	releases, err := s.searchBoth(ctx, "tvsearch", idQuery, prowlarr.TVCats, queries...)
	if err != nil {
		return nil, err
	}
	current, currentSrc := "", ""
	if ep.FilePath != "" {
		current, currentSrc = ep.Quality, ep.Source
	}
	// a release spanning several episodes carries several episodes of bytes —
	// the size band scales with the candidate's own span
	wantFor := func(p parser.Result) quality.Want {
		return quality.Want{
			RuntimeMin: ep.Runtime, HasFile: ep.FilePath != "",
			CurrentQuality: current, CurrentSource: currentSrc,
			Episodes: max(p.Ep.EpisodeEnd-p.Ep.Episode+1, 1),
		}
	}
	return judge(releases, s.protocols(), profile, wantFor, func(_ prowlarr.Release, p parser.Result) string {
		if p.Kind != "episode" {
			return "not an episode release"
		}
		if !titleMatchShow(p.Title, &sh.Show) {
			return "title does not match"
		}
		if p.Year > 0 && sh.Year > 0 && !yearIsTitle(p.Year, sh.Title) && absInt(p.Year-sh.Year) > 1 {
			return fmt.Sprintf("wrong year (%d)", p.Year)
		}
		// a season subtitle in the name outranks the numbers, both ways
		if n, ok := subtitleSeason(sh, p.Title); ok && n != season {
			return fmt.Sprintf("names season %d's story", n)
		}
		pc := sceneToCatalog(sh, p)
		if pc.Ep.Season != season || episode < pc.Ep.Episode || episode > pc.Ep.EpisodeEnd {
			return "wrong episode"
		}
		if episodeTitleConflict(p.Ep.Title, ep.Title) {
			return fmt.Sprintf("names a different episode (%s)", p.Ep.Title)
		}
		return ""
	}), nil
}

// SeasonReleases searches for a whole-season pack. Packs are judged as a
// fill (no upgrade gate against any single episode's file), with the size
// band spread across the season's episode count.
func (s *Service) SeasonReleases(ctx context.Context, sh *catalog.ShowDetails, season int) ([]ReleaseView, error) {
	profile, err := s.resolveProfile(sh.ProfileID, sh.LibraryID, "shows")
	if err != nil {
		return nil, err
	}
	var episodes []catalog.Episode
	for _, se := range sh.Seasons {
		if se.Number == season {
			episodes = se.Episodes
		}
	}
	if len(episodes) == 0 {
		return nil, fmt.Errorf("%w: no season %d on this show", ErrUnknownTarget, season)
	}
	// a season the scene splits in two is asked for under both its numbers
	relSeasons := releaseSeasons(sh, season)
	var queries []string
	for _, n := range relSeasons {
		queries = append(queries, fmt.Sprintf("%s S%02d", queryTitle(sh.Title), n))
	}
	if sub, ok := seasonSubtitleQuery(sh, season); ok {
		queries = append(queries, sub+" S01")
	}
	idQuery := ""
	if sh.TvdbID > 0 {
		idQuery = fmt.Sprintf("{tvdbid:%d}{season:%d}", sh.TvdbID, relSeasons[0])
	}
	releases, err := s.searchBoth(ctx, "tvsearch", idQuery, prowlarr.TVCats, queries...)
	if err != nil {
		return nil, err
	}
	wantFor := func(parser.Result) quality.Want {
		return quality.Want{RuntimeMin: typicalRuntime(episodes), Episodes: len(episodes)}
	}
	return judge(releases, s.protocols(), profile, wantFor, func(_ prowlarr.Release, p parser.Result) string {
		if p.Kind != "season" {
			return "not a season pack"
		}
		if !titleMatchShow(p.Title, &sh.Show) {
			return "title does not match"
		}
		if p.Year > 0 && sh.Year > 0 && !yearIsTitle(p.Year, sh.Title) && absInt(p.Year-sh.Year) > 1 {
			return fmt.Sprintf("wrong year (%d)", p.Year)
		}
		if n, ok := subtitleSeason(sh, p.Title); ok && n != season {
			return fmt.Sprintf("names season %d's story", n)
		}
		if sceneToCatalog(sh, p).Ep.Season != season {
			return "wrong season"
		}
		return ""
	}), nil
}

// judge parses and scores every release. The match callback returns a
// rejection reason for anything that isn't the wanted title, before the
// profile ever weighs in; it sees the raw release too, for the ids some
// indexers attach.
func judge(releases []prowlarr.Release, allowed policy, profile *quality.Profile,
	wantFor func(parser.Result) quality.Want,
	match func(prowlarr.Release, parser.Result) string) []ReleaseView {
	out := make([]ReleaseView, 0, len(releases))
	for _, rel := range releases {
		p := parser.Parse(rel.Title)
		v := ReleaseView{
			Title: rel.Title, Indexer: rel.Indexer, Protocol: rel.Protocol,
			PublishDate: rel.PublishDate, DownloadURL: rel.DownloadURL, Size: rel.Size,
			Quality: p.Quality, Source: p.Source, Languages: p.Languages,
			HDR: p.HDR, Proper: p.Proper,
		}
		// a protocol with no client, or one that has been switched off,
		// is rejected here rather than hidden: the row still shows, and
		// says why it cannot be grabbed
		if reason := allowed.refusal(rel.Protocol); reason != "" {
			v.Reason = reason
		} else if reason := match(rel, p); reason != "" {
			v.Reason = reason
		} else {
			d := quality.Evaluate(quality.Candidate{Result: p, Name: rel.Title, SizeBytes: rel.Size}, profile, wantFor(p))
			v.Accepted, v.Score, v.Reason, v.Terms = d.Accepted, d.Score, d.Reason, d.Terms
			v.FormatScore = d.FormatScore
		}
		out = append(out, v)
	}
	// best first; rejected rows trail in arrival order for the log-like view
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Accepted != out[j].Accepted {
			return out[i].Accepted
		}
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		// Only now does the preference get a say. It breaks a tie
		// between two equally good releases rather than outranking
		// quality: preferring torrents should not mean taking a worse
		// one, only choosing the torrent when there is nothing in it.
		if allowed.prefer != "" && out[i].Protocol != out[j].Protocol {
			return out[i].Protocol == allowed.prefer
		}
		return false
	})
	return out
}

// resolveProfile walks title → library → any profile, so a title orphaned
// by a deleted profile still searches sanely. kind ("movies" or "shows")
// picks which custom formats ride along: formats are scoped, and a movie
// hunt must never be judged by show formats.
func (s *Service) resolveProfile(profileID, libraryID int64, kind string) (*quality.Profile, error) {
	p := s.baseProfile(profileID, libraryID)
	if p == nil {
		return nil, errors.New("no quality profile available — create one in Settings")
	}
	if err := s.Catalog.AttachFormats(p, kind); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) baseProfile(profileID, libraryID int64) *quality.Profile {
	if profileID > 0 {
		if p, err := s.Catalog.GetProfile(profileID); err == nil {
			return p
		}
	}
	if libraryID > 0 {
		if lib, err := s.Catalog.GetLibrary(libraryID); err == nil && lib.QualityProfileID > 0 {
			if p, err := s.Catalog.GetProfile(lib.QualityProfileID); err == nil {
				return p
			}
		}
	}
	profiles, err := s.Catalog.ListProfiles()
	if err != nil || len(profiles) == 0 {
		return nil
	}
	return &profiles[0]
}

func findEpisode(sh *catalog.ShowDetails, season, episode int) *catalog.Episode {
	for _, se := range sh.Seasons {
		if se.Number != season {
			continue
		}
		for i := range se.Episodes {
			if se.Episodes[i].Episode == episode {
				return &se.Episodes[i]
			}
		}
	}
	return nil
}

// typicalRuntime picks the season's median episode runtime — robust against
// an oversized premiere or a few unknowns.
func typicalRuntime(episodes []catalog.Episode) int {
	known := []int{}
	for _, e := range episodes {
		if e.Runtime > 0 {
			known = append(known, e.Runtime)
		}
	}
	if len(known) == 0 {
		return 0
	}
	sort.Ints(known)
	return known[len(known)/2]
}

// Some indexer backends map postings to the databases and Prowlarr relays
// those ids on each release. For MOVIES those mappings are first-party
// evidence — release groups put the IMDb link in the NFO themselves — so a
// release whose every reported id points at a different movie is
// positively the wrong thing, however perfect its name looks: two
// productions sharing a title and year ("The Odyssey", twice in 2026)
// fool the parser but never the ids. When no id is reported nothing
// changes; this is a veto, not a gate, so an early release with no
// mapping yet still gets through on its name.
//
// Shows get NO such veto, on hard-won evidence: TV entries merge, split
// and rename ("Monster (2022)" was once "DAHMER — Monster: The Jeffrey
// Dahmer Story"), and indexers map TV releases by name, often to an
// entry that has since been folded into another — so a foreign tvdb id
// on a correctly-named release is common and proves nothing. Vetoing on
// it silently emptied real shows' searches.

// movieAvailable reports whether the automatic paths may take a release
// for this movie yet. Before a movie is actually out, every release
// claiming it is mislabeled or fake — the window the fake-release farms
// live in — so with min_availability 'released' (the default) automation
// waits for the digital date. TMDB never learns a digital date for many
// titles, so a theatrical release more than ninety days past counts too,
// and a movie with no dates at all is left ungated rather than stranded.
// This gates only what automation takes: a rejected row still shows in
// interactive search with its reason, grabbable by hand. Nothing exempts
// a release from the gate — not even an agreeing reported id, because a
// movie's id mapping comes from the release's own NFO, which a faker
// writes: an id can say which movie a release claims to be, never that
// the movie is actually out.
func movieAvailable(m *catalog.Movie, today string) bool {
	if m.MinAvailability == "announced" {
		return true
	}
	if d := m.DigitalRelease; len(d) >= 10 && d[:10] <= today {
		return true
	}
	if r := m.ReleaseDate; len(r) >= 10 {
		t, err := time.Parse("2006-01-02", r[:10])
		return err == nil && t.AddDate(0, 0, 90).Format("2006-01-02") <= today
	}
	// no dates at all: sparse metadata must not strand a real title
	return m.DigitalRelease == "" && m.ReleaseDate == ""
}

// movieIDAgrees reports whether any id the release carries names exactly
// this movie — Radarr's first matching step, outranking every name
// heuristic.
func movieIDAgrees(rel prowlarr.Release, tmdbID int, imdbID string) bool {
	return (rel.TmdbID > 0 && rel.TmdbID == tmdbID) ||
		(rel.ImdbID > 0 && rel.ImdbID == imdbNum(imdbID))
}

// movieIDMismatch returns a rejection reason when the release's reported
// ids identify a different movie. Any agreeing id clears it outright — an
// indexer that got one id right and one wrong is still talking about the
// right movie.
func movieIDMismatch(rel prowlarr.Release, tmdbID int, imdbID string) string {
	if movieIDAgrees(rel, tmdbID, imdbID) {
		return ""
	}
	if rel.TmdbID > 0 && tmdbID > 0 {
		return fmt.Sprintf("the indexer says this is a different movie (tmdb %d)", rel.TmdbID)
	}
	if rel.ImdbID > 0 && imdbNum(imdbID) > 0 {
		return fmt.Sprintf("the indexer says this is a different movie (tt%07d)", rel.ImdbID)
	}
	return ""
}

// imdbNum strips an IMDb id to the number Prowlarr reports: "tt1375666" →
// 1375666. 0 for none or unparseable.
func imdbNum(s string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "tt"))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// parenYear is TVDB's disambiguating year suffix on same-named shows —
// part of the canonical title ("Monster (2022)"), never part of a release
// name or a useful search keyword in parenthesized form.
var parenYear = regexp.MustCompile(`\s*\(((?:19|20)\d\d)\)\s*$`)

// queryTitle is the show title as sent to indexers' free-text search: the
// disambiguating year keeps its digits but loses the parentheses —
// "Monster (2022)" searches poorly, "Monster 2022" matches how release
// names carry a year.
func queryTitle(title string) string {
	return parenYear.ReplaceAllString(title, " $1")
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// accentFold maps the Latin accents TMDB titles carry onto the plain ASCII
// release names use — "90 Day Fiancé" must equal "90.Day.Fiance".
var accentFold = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a",
	"è", "e", "é", "e", "ê", "e", "ë", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o",
	"ù", "u", "ú", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n", "ý", "y", "ÿ", "y", "ß", "ss",
)

// titleMatch compares titles the way release names mangle them: lowercased,
// accents folded, punctuation collapsed, a leading article dropped. It is
// whole-title equality — never prefix or contains — so a spinoff whose name
// extends the original ("… The Other Way") can never match its parent.
func titleMatch(a, b string) bool {
	return normalizeTitle(a) == normalizeTitle(b)
}

// episodeTitleConflict reports whether a release that carries its own
// episode-title words names a DIFFERENT episode than the expected title.
// Two shows sharing a name ("Wife Swap" 2004 and 2019) produce identical
// Title+SxxExx forms; the episode words in the release are the one
// distinguishing signal a yearless name carries. Conflict needs BOTH
// sides to carry words and the release side to carry at least two: a
// lone unrecognized token after the marker ("Yes.Dear.S03E09.HBTV") is
// far more often mangled scene junk than a title, and one word is not
// enough evidence to refuse a correctly-numbered release over.
func episodeTitleConflict(got, want string) bool {
	g, w := significantWords(got), significantWords(want)
	if len(g) < 2 || len(w) == 0 {
		return false
	}
	for t := range g {
		if w[t] {
			return false
		}
	}
	return true
}

// significantWords tokenizes a normalized title, dropping connectives,
// numbering, and pure digits — "Part 2" carries no distinguishing words,
// and "Floyd-Ely Vs. Clanton" keeps floyd/ely/clanton.
func significantWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.Fields(normalizeTitle(s)) {
		switch t {
		case "vs", "v", "and", "the", "a", "an", "of", "in", "part", "pt", "episode", "ep":
			continue
		}
		if _, err := strconv.Atoi(t); err == nil {
			continue
		}
		out[t] = true
	}
	return out
}

func normalizeTitle(s string) string {
	s = strings.ToLower(s)
	s = accentFold.Replace(s)
	// apostrophes vanish rather than becoming spaces: release names write
	// "It's" as "Its", and "it s" would never equal "its"
	s = apostrophes.Replace(s)
	// "&" and "and" are the same word in a title
	s = strings.ReplaceAll(s, "&", " and ")
	s = nonWord.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "the ")
	return strings.TrimSpace(s)
}

// apostrophes covers the straight, curly, and backtick forms titles and
// release names mix freely.
var apostrophes = strings.NewReplacer("'", "", "’", "", "‘", "", "`", "")

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
