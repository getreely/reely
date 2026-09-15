// Package metadata talks to TMDB — the sole metadata authority for movies,
// shows, episodes, people, and artwork. Each install uses its own free API
// key (sealed at rest in settings); there is no shared proxy service.
package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const tmdbBase = "https://api.themoviedb.org/3"

// ImageBase is the CDN prefix for poster_path / backdrop_path /
// profile_path values ("w500" fits grid posters; detail pages can go bigger).
const ImageBase = "https://image.tmdb.org/t/p"

type TMDB struct {
	// key returns the current API key on every call, so a key saved in
	// Settings takes effect without a restart.
	key    func() string
	base   string
	client *http.Client
}

func NewTMDB(key func() string) *TMDB {
	return &TMDB{key: key, base: tmdbBase, client: &http.Client{Timeout: 15 * time.Second}}
}

// SetBaseURL points the client somewhere else — tests aim it at a local
// fake; production never calls this.
func (t *TMDB) SetBaseURL(u string) { t.base = u }

// Configured reports whether an API key is present at all — handlers use it
// to answer "is search even possible" without spending a request.
func (t *TMDB) Configured() bool { return t.key() != "" }

// SearchResult is one row from a movie or TV search.
type SearchResult struct {
	TmdbID int `json:"tmdbId"`
	// TvdbID is set on results from the TVDB client — the id the add flow
	// uses when shows are sourced from TVDB.
	TvdbID     int     `json:"tvdbId,omitempty"`
	Kind       string  `json:"kind"` // movie | show
	Title      string  `json:"title"`
	Year       int     `json:"year"`
	Overview   string  `json:"overview"`
	Poster     string  `json:"poster"`
	Popularity float64 `json:"popularity"`
	// Anime marks Japanese animation, so the discovery rows can leave it
	// out for households that never want it. Set on the list endpoints,
	// which carry the fields it is derived from; plain search leaves it
	// false, since searching for a title by name is always deliberate.
	Anime bool `json:"anime,omitempty"`
	// English marks titles in English or of stated US origin — the other
	// discovery filter. Same rules about where it is set.
	English bool `json:"english,omitempty"`
}

// isEnglish keeps what an English-speaking household is actually looking
// for. The language does the work: TMDB's global charts carry a lot of
// Korean, Spanish, Turkish, and Hindi titles, and those are what a US
// viewer wants out of the way. Stated US origin is an OR rather than an
// AND, so an American title TMDB files under another original language
// still counts — and British and Australian titles deliberately survive,
// since a "top shows" row without The Crown is worse, not better.
func isEnglish(originalLanguage string, originCountries []string) bool {
	// nothing to judge on: keep it. A filter that removes things has to act
	// on positive evidence only — treating unknown as foreign would quietly
	// empty a row the day TMDB omits a field.
	if originalLanguage == "" && len(originCountries) == 0 {
		return true
	}
	if originalLanguage == "en" {
		return true
	}
	for _, c := range originCountries {
		if c == "US" {
			return true
		}
	}
	return false
}

// animationGenre is TMDB's genre id for Animation, on both movies and TV.
const animationGenre = 16

// isAnime is the heuristic the wider self-hosting world settled on:
// animation AND Japanese origin. Genre alone would sweep up Pixar and
// every cartoon; origin alone would sweep up live-action Japanese cinema.
// Together they are precise enough to act on, and wrong rarely enough that
// the row is still worth trusting.
func isAnime(genreIDs []int, originalLanguage string, originCountries []string) bool {
	animated := false
	for _, id := range genreIDs {
		if id == animationGenre {
			animated = true
			break
		}
	}
	if !animated {
		return false
	}
	if originalLanguage == "ja" {
		return true
	}
	for _, c := range originCountries {
		if c == "JP" {
			return true
		}
	}
	return false
}

// SearchMovies queries TMDB movie search. Year 0 searches without a filter.
func (t *TMDB) SearchMovies(ctx context.Context, query string, year int) ([]SearchResult, error) {
	q := url.Values{"query": {query}}
	if year > 0 {
		q.Set("year", strconv.Itoa(year))
	}
	var body struct {
		Results []struct {
			ID          int     `json:"id"`
			Title       string  `json:"title"`
			ReleaseDate string  `json:"release_date"`
			Overview    string  `json:"overview"`
			PosterPath  string  `json:"poster_path"`
			Popularity  float64 `json:"popularity"`
			Adult       bool    `json:"adult"`
		} `json:"results"`
	}
	if err := t.get(ctx, "/search/movie", q, &body); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(body.Results))
	for _, r := range body.Results {
		if r.Adult {
			continue
		}
		out = append(out, SearchResult{
			TmdbID: r.ID, Kind: "movie", Title: r.Title, Year: yearOf(r.ReleaseDate),
			Overview: r.Overview, Poster: r.PosterPath, Popularity: r.Popularity,
		})
	}
	return out, nil
}

// SearchShows queries TMDB TV search. Year 0 searches without a filter.
func (t *TMDB) SearchShows(ctx context.Context, query string, year int) ([]SearchResult, error) {
	q := url.Values{"query": {query}}
	if year > 0 {
		q.Set("first_air_date_year", strconv.Itoa(year))
	}
	var body struct {
		Results []struct {
			ID           int      `json:"id"`
			Name         string   `json:"name"`
			FirstAirDate string   `json:"first_air_date"`
			Overview     string   `json:"overview"`
			PosterPath   string   `json:"poster_path"`
			Popularity   float64  `json:"popularity"`
			GenreIDs     []int    `json:"genre_ids"`
			OrigLanguage string   `json:"original_language"`
			OrigCountry  []string `json:"origin_country"` // tv
			Adult        bool     `json:"adult"`
		} `json:"results"`
	}
	if err := t.get(ctx, "/search/tv", q, &body); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(body.Results))
	for _, r := range body.Results {
		if r.Adult {
			continue
		}
		out = append(out, SearchResult{
			TmdbID: r.ID, Kind: "show", Title: r.Name, Year: yearOf(r.FirstAirDate),
			Overview: r.Overview, Poster: r.PosterPath, Popularity: r.Popularity,
		})
	}
	return out, nil
}

// The discovery charts — trending, popular, top rated — differ only in
// their endpoint, so one fetch-and-convert serves all three.
type chartRow struct {
	ID           int      `json:"id"`
	Title        string   `json:"title"` // movies
	Name         string   `json:"name"`  // tv
	ReleaseDate  string   `json:"release_date"`
	FirstAirDate string   `json:"first_air_date"`
	Overview     string   `json:"overview"`
	PosterPath   string   `json:"poster_path"`
	Popularity   float64  `json:"popularity"`
	GenreIDs     []int    `json:"genre_ids"`
	OrigLanguage string   `json:"original_language"`
	OrigCountry  []string `json:"origin_country"` // tv only
	Adult        bool     `json:"adult"`
}

func (t *TMDB) chart(ctx context.Context, path, kind string) ([]SearchResult, error) {
	return t.chartQuery(ctx, path, kind, url.Values{})
}

func (t *TMDB) chartQuery(ctx context.Context, path, kind string, q url.Values) ([]SearchResult, error) {
	var body struct {
		Results []chartRow `json:"results"`
	}
	if err := t.get(ctx, path, q, &body); err != nil {
		return nil, err
	}
	outKind := "movie"
	if kind == "tv" {
		outKind = "show"
	}
	out := make([]SearchResult, 0, len(body.Results))
	for _, r := range body.Results {
		if r.Adult {
			continue
		}
		out = append(out, SearchResult{
			TmdbID: r.ID, Kind: outKind,
			Title:    firstNonEmpty(r.Title, r.Name),
			Year:     yearOf(firstNonEmpty(r.ReleaseDate, r.FirstAirDate)),
			Overview: r.Overview, Poster: r.PosterPath, Popularity: r.Popularity,
			Anime:   isAnime(r.GenreIDs, r.OrigLanguage, r.OrigCountry),
			English: isEnglish(r.OrigLanguage, r.OrigCountry),
		})
	}
	return out, nil
}

// Trending is the week's movers for one kind ("movie" or "tv").
func (t *TMDB) Trending(ctx context.Context, kind string) ([]SearchResult, error) {
	return t.chart(ctx, fmt.Sprintf("/trending/%s/week", kind), kind)
}

// Popular is steadier than trending, which chases the week's spikes.
func (t *TMDB) Popular(ctx context.Context, kind string) ([]SearchResult, error) {
	return t.chart(ctx, fmt.Sprintf("/%s/popular", kind), kind)
}

// Similar lists what TMDB thinks pairs with one title — the "more like
// this" row. Recommendations are TMDB's blend of editorial and behaviour,
// which reads better than raw genre similarity.
func (t *TMDB) Similar(ctx context.Context, kind string, tmdbID int) ([]SearchResult, error) {
	return t.chart(ctx, fmt.Sprintf("/%s/%d/recommendations", kind, tmdbID), kind)
}

// StreamingProvider is one service reely offers a row for. The ids are
// TMDB's watch-provider ids, and the region scopes availability — a
// service's catalogue differs by country, so "on Netflix" is only a
// meaningful claim with one attached.
type StreamingProvider struct {
	Key  string // stable id for the UI
	Name string
	ID   int
}

// StreamingProviders are the services worth a row. Deliberately short:
// each one costs a TMDB call per kind on every cache refresh, and a wall
// of half-empty rows helps nobody.
var StreamingProviders = []StreamingProvider{
	{Key: "netflix", Name: "Netflix", ID: 8},
	{Key: "hulu", Name: "Hulu", ID: 15},
	{Key: "max", Name: "Max", ID: 1899},
	{Key: "disney", Name: "Disney+", ID: 337},
	{Key: "prime", Name: "Prime Video", ID: 9},
	{Key: "appletv", Name: "Apple TV+", ID: 350},
}

// OnProvider lists what is popular on one streaming service for one kind.
// TMDB's discover endpoint does the filtering, so this is what the service
// actually carries in that region rather than a guess.
func (t *TMDB) OnProvider(ctx context.Context, kind string, providerID int, region string) ([]SearchResult, error) {
	if region == "" {
		region = "US"
	}
	q := url.Values{
		"with_watch_providers": {strconv.Itoa(providerID)},
		"watch_region":         {region},
		"sort_by":              {"popularity.desc"},
	}
	return t.chartQuery(ctx, fmt.Sprintf("/discover/%s", kind), kind, q)
}

// TopRated is the standing best-reviewed list — a different shape from
// popular: older, steadier, and far less driven by what just came out.
func (t *TMDB) TopRated(ctx context.Context, kind string) ([]SearchResult, error) {
	return t.chart(ctx, fmt.Sprintf("/%s/top_rated", kind), kind)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Person is a cast member with their role on one title.
type Person struct {
	TmdbID    int    `json:"tmdbId"`
	Name      string `json:"name"`
	Character string `json:"character"`
	Photo     string `json:"photo"`
	Order     int    `json:"order"`
}

// MovieDetail is the full movie record used on import: metadata, artwork,
// the IMDb id, and the top-billed cast.
type MovieDetail struct {
	TmdbID   int
	Title    string
	Year     int
	Overview string
	Runtime  int
	Genres   []string
	// Aliases are the movie's other names — TMDB's alternative titles and
	// the original-language title — the names releases carry when they
	// don't use the canonical one.
	Aliases        []string
	ReleaseDate    string
	DigitalRelease string
	Poster         string
	Backdrop       string
	ImdbID         string
	Cast           []Person
}

// Movie fetches one movie with credits, release dates, and external ids in a
// single request (append_to_response). DigitalRelease is TMDB release type 4.
func (t *TMDB) Movie(ctx context.Context, id int) (*MovieDetail, error) {
	var body struct {
		Adult         bool   `json:"adult"`
		Title         string `json:"title"`
		OriginalTitle string `json:"original_title"`
		ReleaseDate   string `json:"release_date"`
		Overview      string `json:"overview"`
		Runtime       int    `json:"runtime"`
		PosterPath    string `json:"poster_path"`
		BackdropPath  string `json:"backdrop_path"`
		Genres        []struct {
			Name string `json:"name"`
		} `json:"genres"`
		AlternativeTitles struct {
			Titles []struct {
				Title string `json:"title"`
			} `json:"titles"`
		} `json:"alternative_titles"`
		ExternalIDs struct {
			ImdbID string `json:"imdb_id"`
		} `json:"external_ids"`
		ReleaseDates struct {
			Results []struct {
				Iso31661     string `json:"iso_3166_1"`
				ReleaseDates []struct {
					Type        int    `json:"type"`
					ReleaseDate string `json:"release_date"`
				} `json:"release_dates"`
			} `json:"results"`
		} `json:"release_dates"`
		Credits struct {
			Cast []struct {
				ID          int    `json:"id"`
				Name        string `json:"name"`
				Character   string `json:"character"`
				ProfilePath string `json:"profile_path"`
				Order       int    `json:"order"`
			} `json:"cast"`
		} `json:"credits"`
	}
	q := url.Values{"append_to_response": {"credits,external_ids,release_dates,alternative_titles"}}
	if err := t.get(ctx, fmt.Sprintf("/movie/%d", id), q, &body); err != nil {
		return nil, err
	}
	// the gate that matters: a tmdb id is guessable, so refusing here is
	// what keeps an adult title out of preview, add and refresh alike
	if body.Adult {
		return nil, ErrAdultContent
	}
	d := &MovieDetail{
		TmdbID: id, Title: body.Title, Year: yearOf(body.ReleaseDate),
		Overview: body.Overview, Runtime: body.Runtime, ReleaseDate: body.ReleaseDate,
		Poster: body.PosterPath, Backdrop: body.BackdropPath, ImdbID: body.ExternalIDs.ImdbID,
		DigitalRelease: digitalRelease(body.ReleaseDates.Results),
	}
	for _, g := range body.Genres {
		d.Genres = append(d.Genres, g.Name)
	}
	// every other name the movie answers to — the original-language title
	// and TMDB's alternative titles — deduped; releases use them freely
	names := []string{body.OriginalTitle}
	for _, alt := range body.AlternativeTitles.Titles {
		names = append(names, alt.Title)
	}
	seen := map[string]bool{body.Title: true}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		d.Aliases = append(d.Aliases, name)
	}
	d.Cast = topCast(body.Credits.Cast)
	return d, nil
}

// digitalRelease finds the earliest US digital release (type 4), falling back
// to any region's digital date — it's the date worth hunting a file by.
func digitalRelease(results []struct {
	Iso31661     string `json:"iso_3166_1"`
	ReleaseDates []struct {
		Type        int    `json:"type"`
		ReleaseDate string `json:"release_date"`
	} `json:"release_dates"`
}) string {
	best := ""
	for _, r := range results {
		for _, rd := range r.ReleaseDates {
			if rd.Type != 4 || rd.ReleaseDate == "" {
				continue
			}
			if r.Iso31661 == "US" {
				return rd.ReleaseDate
			}
			if best == "" || rd.ReleaseDate < best {
				best = rd.ReleaseDate
			}
		}
	}
	return best
}

// ShowDetail is a show plus its full episode list, used on import.
type ShowDetail struct {
	TmdbID   int
	Title    string
	Year     int
	Overview string
	Status   string
	Genres   []string
	// Aliases are the show's other names (TVDB's aliases array; TMDB
	// leaves them empty) — the titles releases actually carry when they
	// don't use the canonical one.
	Aliases  []string
	Poster   string
	Backdrop string
	ImdbID   string
	TvdbID   int
	Cast     []Person
	Seasons  []SeasonDetail
}

type SeasonDetail struct {
	Number   int
	Name     string
	Episodes []EpisodeDetail
}

type EpisodeDetail struct {
	TmdbID   int
	Season   int
	Episode  int
	Title    string
	Overview string
	AirDate  string
	Runtime  int // minutes; 0 when TMDB doesn't know
}

// Show fetches a show's top-level record (credits + external ids), then each
// season's episode list. Season 0 (specials) is skipped.
func (t *TMDB) Show(ctx context.Context, id int) (*ShowDetail, error) {
	var head struct {
		Adult        bool   `json:"adult"`
		Name         string `json:"name"`
		FirstAirDate string `json:"first_air_date"`
		Overview     string `json:"overview"`
		Status       string `json:"status"`
		PosterPath   string `json:"poster_path"`
		BackdropPath string `json:"backdrop_path"`
		Genres       []struct {
			Name string `json:"name"`
		} `json:"genres"`
		ExternalIDs struct {
			ImdbID string `json:"imdb_id"`
			TvdbID int    `json:"tvdb_id"`
		} `json:"external_ids"`
		Credits struct {
			Cast []struct {
				ID          int    `json:"id"`
				Name        string `json:"name"`
				Character   string `json:"character"`
				ProfilePath string `json:"profile_path"`
				Order       int    `json:"order"`
			} `json:"cast"`
		} `json:"credits"`
		Seasons []struct {
			SeasonNumber int `json:"season_number"`
		} `json:"seasons"`
	}
	q := url.Values{"append_to_response": {"credits,external_ids"}}
	if err := t.get(ctx, fmt.Sprintf("/tv/%d", id), q, &head); err != nil {
		return nil, err
	}
	if head.Adult {
		return nil, ErrAdultContent
	}
	d := &ShowDetail{
		TmdbID: id, Title: head.Name, Year: yearOf(head.FirstAirDate),
		Overview: head.Overview, Status: head.Status,
		Poster: head.PosterPath, Backdrop: head.BackdropPath, ImdbID: head.ExternalIDs.ImdbID,
		// the TVDB id rides along from TMDB's external ids: it is the key
		// most usenet indexers accept for TV searches, so reely can search
		// by id the way Sonarr does without a TheTVDB integration
		TvdbID: head.ExternalIDs.TvdbID,
	}
	for _, g := range head.Genres {
		d.Genres = append(d.Genres, g.Name)
	}
	d.Cast = topCast(head.Credits.Cast)

	for _, s := range head.Seasons {
		if s.SeasonNumber == 0 {
			continue // specials
		}
		season, err := t.season(ctx, id, s.SeasonNumber)
		if err != nil {
			return nil, err
		}
		d.Seasons = append(d.Seasons, *season)
	}
	return d, nil
}

func (t *TMDB) season(ctx context.Context, showID, number int) (*SeasonDetail, error) {
	var body struct {
		Name     string `json:"name"`
		Episodes []struct {
			ID            int    `json:"id"`
			EpisodeNumber int    `json:"episode_number"`
			Name          string `json:"name"`
			Overview      string `json:"overview"`
			AirDate       string `json:"air_date"`
			Runtime       int    `json:"runtime"`
		} `json:"episodes"`
	}
	if err := t.get(ctx, fmt.Sprintf("/tv/%d/season/%d", showID, number), url.Values{}, &body); err != nil {
		return nil, err
	}
	s := &SeasonDetail{Number: number, Name: body.Name}
	for _, e := range body.Episodes {
		s.Episodes = append(s.Episodes, EpisodeDetail{
			TmdbID: e.ID, Season: number, Episode: e.EpisodeNumber,
			Title: e.Name, Overview: e.Overview, AirDate: e.AirDate, Runtime: e.Runtime,
		})
	}
	return s, nil
}

// topCast keeps the billed leads (order < 15) — enough for a title's cast row
// and people pages without storing the whole crew.
func topCast(cast []struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
}) []Person {
	var out []Person
	for _, c := range cast {
		if c.Order >= 15 {
			continue
		}
		out = append(out, Person{
			TmdbID: c.ID, Name: c.Name, Character: c.Character,
			Photo: c.ProfilePath, Order: c.Order,
		})
	}
	return out
}

func (t *TMDB) get(ctx context.Context, path string, q url.Values, into any) error {
	key := t.key()
	if key == "" {
		return fmt.Errorf("no TMDB API key configured")
	}
	q.Set("api_key", key)
	// every TMDB request, so no endpoint can forget it; TMDB ignores the
	// parameter where it doesn't apply
	q.Set("include_adult", "false")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("TMDB rejected the API key")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TMDB %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}
