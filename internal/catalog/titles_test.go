package catalog

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
)

// A late-arriving episode inherits its season's monitor state: a silenced
// season stays silent when TMDB adds stragglers, while a brand-new season
// arrives monitored.
func TestUpsertShowNewEpisodesInheritSeasonMonitoring(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := New(conn)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}

	detail := &metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "Pilot"},
			{TmdbID: 2, Season: 1, Episode: 2, Title: "Cat's in the Bag..."},
		}}},
	}
	showID, err := cat.UpsertShow(detail, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.SetSeasonMonitored(showID, 1, false); err != nil {
		t.Fatal(err)
	}

	// the refresh brings a straggler for the silenced season and a whole
	// new season
	detail.Seasons[0].Episodes = append(detail.Seasons[0].Episodes,
		metadata.EpisodeDetail{TmdbID: 3, Season: 1, Episode: 3, Title: "...And the Bag's in the River"})
	detail.Seasons = append(detail.Seasons, metadata.SeasonDetail{
		Number: 2, Name: "Season 2", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 4, Season: 2, Episode: 1, Title: "Seven Thirty-Seven"},
		}})
	if _, err := cat.UpsertShow(detail, lib.ID); err != nil {
		t.Fatal(err)
	}

	sh, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	monitored := map[[2]int]bool{}
	for _, se := range sh.Seasons {
		for _, ep := range se.Episodes {
			monitored[[2]int{ep.Season, ep.Episode}] = ep.Monitored
		}
	}
	if monitored[[2]int{1, 3}] {
		t.Fatal("straggler in a silenced season arrived monitored")
	}
	if !monitored[[2]int{2, 1}] {
		t.Fatal("brand-new season arrived unmonitored")
	}
	// existing rows keep their state on refresh
	if monitored[[2]int{1, 1}] || monitored[[2]int{1, 2}] {
		t.Fatal("refresh re-monitored silenced episodes")
	}
}

// A season pack landing is one home-view card, not one per episode; a
// second show's later import leads the list; deleted files drop out.
func TestRecentShowImportsGroupPerShow(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := New(conn)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	seed := func(tmdb int, title string, eps int) (int64, []int64) {
		t.Helper()
		var list []metadata.EpisodeDetail
		for i := 1; i <= eps; i++ {
			list = append(list, metadata.EpisodeDetail{TmdbID: tmdb*100 + i, Season: 1, Episode: i})
		}
		id, err := cat.UpsertShow(&metadata.ShowDetail{
			TmdbID: tmdb, Title: title,
			Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: list}},
		}, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for i := 1; i <= eps; i++ {
			epID, err := cat.EpisodeID(id, 1, i)
			if err != nil || epID == 0 {
				t.Fatalf("episode id: %v", err)
			}
			ids = append(ids, epID)
		}
		return id, ids
	}
	imp := func(showID, epID int64, path string) {
		t.Helper()
		if err := cat.AttachEpisodeFile(epID, path, 1, "1080p", "web"); err != nil {
			t.Fatal(err)
		}
		if err := cat.AddHistory("imported", 0, showID, epID, "{}"); err != nil {
			t.Fatal(err)
		}
	}

	// a whole season of show A, then one episode of show B
	aID, aEps := seed(1, "Season Pack Show", 5)
	for i, ep := range aEps {
		imp(aID, ep, filepath.Join("/x/a", string(rune('a'+i))+".mkv"))
	}
	bID, bEps := seed(2, "Weekly Show", 2)
	imp(bID, bEps[0], "/x/b1.mkv")

	out, err := cat.RecentShowImports(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("cards = %d (%+v), want 2", len(out), out)
	}
	if out[0].ShowID != bID || out[0].Count != 1 || out[0].Episode != 1 {
		t.Fatalf("first card = %+v, want Weekly Show x1", out[0])
	}
	if out[1].ShowID != aID || out[1].Count != 5 {
		t.Fatalf("second card = %+v, want Season Pack Show x5", out[1])
	}

	// detaching the weekly show's file drops its card
	if err := cat.AttachEpisodeFile(bEps[0], "", 0, "", ""); err != nil {
		t.Fatal(err)
	}
	out, err = cat.RecentShowImports(20)
	if err != nil || len(out) != 1 || out[0].ShowID != aID {
		t.Fatalf("after detach: %+v (%v)", out, err)
	}
}

// The reported regression: the card list used to be built from the newest
// 400 import EVENTS, so a bulk backfill for a couple of shows pushed every
// other show off the home row. The limit must apply to shows.
func TestRecentShowImportsSurviveBulkBackfill(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := New(conn)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	seed := func(tmdb int, title string, eps int) (int64, []int64) {
		t.Helper()
		var list []metadata.EpisodeDetail
		for i := 1; i <= eps; i++ {
			list = append(list, metadata.EpisodeDetail{TmdbID: tmdb*1000 + i, Season: 1, Episode: i})
		}
		id, err := cat.UpsertShow(&metadata.ShowDetail{
			TmdbID: tmdb, Title: title,
			Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: list}},
		}, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for i := 1; i <= eps; i++ {
			epID, err := cat.EpisodeID(id, 1, i)
			if err != nil || epID == 0 {
				t.Fatalf("episode id: %v", err)
			}
			ids = append(ids, epID)
		}
		return id, ids
	}

	oldID, oldEps := seed(11, "Older Show", 1)
	if err := cat.AttachEpisodeFile(oldEps[0], "/x/old.mkv", 1, "1080p", "web"); err != nil {
		t.Fatal(err)
	}
	if err := cat.AddHistory("imported", 0, oldID, oldEps[0], "{}"); err != nil {
		t.Fatal(err)
	}

	// then one show backfills hard: 450 import events land after it
	bulkID, bulkEps := seed(12, "Backfill Show", 450)
	for _, ep := range bulkEps {
		if err := cat.AttachEpisodeFile(ep, fmt.Sprintf("/x/bulk-%d.mkv", ep), 1, "1080p", "web"); err != nil {
			t.Fatal(err)
		}
		if err := cat.AddHistory("imported", 0, bulkID, ep, "{}"); err != nil {
			t.Fatal(err)
		}
	}

	out, err := cat.RecentShowImports(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("cards = %d, want 2 — the bulk import evicted the older show", len(out))
	}
	if out[0].ShowID != bulkID || out[0].Count != 450 {
		t.Fatalf("first card = %+v, want the backfill show x450", out[0])
	}
	if out[1].ShowID != oldID || out[1].Count != 1 {
		t.Fatalf("second card = %+v, want the older show still present", out[1])
	}
}
