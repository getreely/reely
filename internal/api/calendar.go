package api

import (
	"net/http"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

// The calendar: what's coming for everything monitored, and what already
// aired but never landed. Library-scoped like every other read.

func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	days := int(queryInt(r, "days"))
	if days <= 0 || days > 90 {
		days = 30
	}
	today := time.Now().Format("2006-01-02")
	ahead := time.Now().AddDate(0, 0, days).Format("2006-01-02")
	back := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	// the calendar answers for the libraries this account can reach and
	// no others — it used to answer for every library on the install
	libs, err := s.visibleLibraries(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	upcoming, err := s.Catalog.Calendar(today, ahead, libs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	recent, err := s.Catalog.Calendar(back, yesterday, libs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// the "aired · still missing" list is the recent window minus anything
	// that made it to disk
	missing := recent[:0]
	for _, it := range recent {
		if !it.OnDisk {
			missing = append(missing, it)
		}
	}

	a := s.access(r)
	libOf := func(it catalog.CalendarItem) int64 { return it.LibraryID }
	writeJSON(w, http.StatusOK, map[string]any{
		"upcoming": dedupeCalendar(filterByAccess(upcoming, a, libOf)),
		"missing":  dedupeCalendar(filterByAccess(missing, a, libOf)),
		// the rows carry poster paths now, and a path is only an image
		// with the base in front of it
		"imageBase": metadata.ImageBase,
	})
}

// dedupeCalendar collapses the same event seen through several libraries —
// a title in three collections releases once, not three times. An on-disk
// sighting wins so the tag reads right.
func dedupeCalendar(items []catalog.CalendarItem) []catalog.CalendarItem {
	type key struct {
		kind, date, title string
		season, episode   int
	}
	index := map[key]int{}
	out := items[:0]
	for _, it := range items {
		k := key{it.Kind, it.Date, it.Title, it.Season, it.Episode}
		if i, ok := index[k]; ok {
			if it.OnDisk {
				out[i].OnDisk = true
			}
			continue
		}
		index[k] = len(out)
		out = append(out, it)
	}
	return out
}
