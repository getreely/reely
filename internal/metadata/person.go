package metadata

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// PersonDetail is one person's page: who they are plus everything they've
// been billed in, from TMDB's combined credits.
type PersonDetail struct {
	TmdbID       int            `json:"tmdbId"`
	Name         string         `json:"name"`
	Biography    string         `json:"biography"`
	Birthday     string         `json:"birthday"`
	Deathday     string         `json:"deathday"`
	PlaceOfBirth string         `json:"placeOfBirth"`
	KnownFor     string         `json:"knownFor"`
	Photo        string         `json:"photo"`
	Credits      []PersonCredit `json:"credits"`
}

// PersonCredit is one title on a filmography.
type PersonCredit struct {
	Kind      string `json:"kind"` // movie | show
	TmdbID    int    `json:"tmdbId"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Character string `json:"character"`
	Poster    string `json:"poster"`
}

// Person fetches one person with their combined movie and TV credits in a
// single request.
func (t *TMDB) Person(ctx context.Context, id int) (*PersonDetail, error) {
	var body struct {
		Name         string `json:"name"`
		Biography    string `json:"biography"`
		Birthday     string `json:"birthday"`
		Deathday     string `json:"deathday"`
		PlaceOfBirth string `json:"place_of_birth"`
		KnownFor     string `json:"known_for_department"`
		ProfilePath  string `json:"profile_path"`
		Credits      struct {
			Cast []struct {
				Adult        bool   `json:"adult"`
				MediaType    string `json:"media_type"`
				ID           int    `json:"id"`
				Title        string `json:"title"` // movies
				Name         string `json:"name"`  // shows
				ReleaseDate  string `json:"release_date"`
				FirstAirDate string `json:"first_air_date"`
				Character    string `json:"character"`
				PosterPath   string `json:"poster_path"`
			} `json:"cast"`
		} `json:"combined_credits"`
	}
	q := url.Values{"append_to_response": {"combined_credits"}}
	if err := t.get(ctx, fmt.Sprintf("/person/%d", id), q, &body); err != nil {
		return nil, err
	}
	d := &PersonDetail{
		TmdbID: id, Name: body.Name, Biography: body.Biography,
		Birthday: body.Birthday, Deathday: body.Deathday, PlaceOfBirth: body.PlaceOfBirth,
		KnownFor: body.KnownFor, Photo: body.ProfilePath,
	}
	// one card per title: a person credited with several roles on one show
	// collapses into a single entry with the characters joined
	index := map[string]int{}
	for _, c := range body.Credits.Cast {
		// combined_credits has no include_adult parameter and TMDB does not
		// filter it, so this is the only thing standing between an actor's
		// adult credits and their filmography page
		if c.Adult {
			continue
		}
		kind, title, year := "", "", 0
		switch c.MediaType {
		case "movie":
			kind, title, year = "movie", c.Title, yearOf(c.ReleaseDate)
		case "tv":
			kind, title, year = "show", c.Name, yearOf(c.FirstAirDate)
		default:
			continue
		}
		if title == "" {
			continue
		}
		key := fmt.Sprintf("%s/%d", kind, c.ID)
		if at, seen := index[key]; seen {
			prev := &d.Credits[at]
			if c.Character != "" && !strings.Contains(prev.Character, c.Character) {
				if prev.Character != "" {
					prev.Character += " / "
				}
				prev.Character += c.Character
			}
			continue
		}
		index[key] = len(d.Credits)
		d.Credits = append(d.Credits, PersonCredit{
			Kind: kind, TmdbID: c.ID, Title: title, Year: year,
			Character: c.Character, Poster: c.PosterPath,
		})
	}
	// newest first; undated credits sink to the end
	sort.SliceStable(d.Credits, func(i, j int) bool {
		a, b := d.Credits[i].Year, d.Credits[j].Year
		if (a == 0) != (b == 0) {
			return b == 0
		}
		return a > b
	})
	return d, nil
}
