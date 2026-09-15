package api

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

// The person page: TMDB's biography and full filmography, cross-linked
// against the library so titles already on disk open in place. Without
// a TMDB key (or when TMDB is down) the page degrades to what the credits
// tables already hold — name, photo, and the library filmography.

// personCredit is one filmography card. LocalID is set only when the title
// is in a library this user may see; the id alone is the "in library" flag.
type personCredit struct {
	Kind      string `json:"kind"` // movie | show
	TmdbID    int    `json:"tmdbId"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Character string `json:"character"`
	Poster    string `json:"poster"`
	LocalID   int64  `json:"localId,omitempty"`
	OnDisk    bool   `json:"onDisk,omitempty"`
}

func (s *Server) handlePerson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r) // a TMDB person id, not a local row id
	if !ok {
		return
	}
	acc := s.access(r)
	local, err := s.Catalog.PersonCredits(int(id))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// out-of-scope titles vanish rather than 403 — same rule as detail pages
	inScope := make([]catalog.LocalCredit, 0, len(local))
	for _, lc := range local {
		if acc.mayLibrary(lc.LibraryID) {
			inScope = append(inScope, lc)
		}
	}

	if s.TMDB.Configured() {
		if d, err := s.TMDB.Person(r.Context(), int(id)); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"person":    d,
				"credits":   mergeCredits(d.Credits, inScope),
				"imageBase": metadata.ImageBase,
			})
			return
		} else if len(inScope) == 0 {
			// nothing stored to fall back on — surface the TMDB failure
			writeErr(w, http.StatusBadGateway, err)
			return
		} else {
			// the error text can carry TMDB's status line — newlines stripped
			// so remote content can't forge extra log lines
			msg := strings.ReplaceAll(err.Error(), "\n", " ")
			log.Printf("api: person %d: TMDB unavailable, serving stored credits: %s", id, msg) //nolint:gosec // G706: sanitized above
		}
	}

	// stored-only: the person exists for us if any library title bills them
	name, photo, err := s.Catalog.StoredPerson(int(id))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	credits := make([]personCredit, 0, len(inScope))
	for _, lc := range inScope {
		credits = append(credits, personCredit{
			Kind: lc.Kind, TmdbID: lc.TmdbID, Title: lc.Title, Year: lc.Year,
			Character: lc.Character, Poster: lc.Poster, LocalID: lc.ID, OnDisk: lc.OnDisk,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"person":    metadata.PersonDetail{TmdbID: int(id), Name: name, Photo: photo},
		"credits":   credits,
		"imageBase": metadata.ImageBase,
	})
}

// mergeCredits lays the library over TMDB's filmography: every TMDB credit
// becomes a card, and the ones matching an in-scope title get its local id
// and on-disk state.
func mergeCredits(remote []metadata.PersonCredit, local []catalog.LocalCredit) []personCredit {
	type key struct {
		kind   string
		tmdbID int
	}
	have := make(map[key]catalog.LocalCredit, len(local))
	for _, lc := range local {
		have[key{lc.Kind, lc.TmdbID}] = lc
	}
	out := make([]personCredit, 0, len(remote))
	for _, rc := range remote {
		pc := personCredit{
			Kind: rc.Kind, TmdbID: rc.TmdbID, Title: rc.Title, Year: rc.Year,
			Character: rc.Character, Poster: rc.Poster,
		}
		if lc, found := have[key{rc.Kind, rc.TmdbID}]; found {
			pc.LocalID = lc.ID
			pc.OnDisk = lc.OnDisk
			if pc.Poster == "" {
				pc.Poster = lc.Poster
			}
		}
		out = append(out, pc)
	}
	return out
}
