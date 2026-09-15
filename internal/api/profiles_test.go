package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/getreely/reely/internal/quality"
)

func TestProfileLifecycle(t *testing.T) {
	h := testServer(t).Handler()

	// the migration seeds one default
	rec, out := doJSON(t, h, "GET", "/api/v1/profiles", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	seeded, _ := out["profiles"].([]any)
	if len(seeded) != 1 {
		t.Fatalf("seeded profiles = %v", out)
	}

	rec, created := doJSON(t, h, "POST", "/api/v1/profiles", map[string]any{
		"name": "4K HDR", "qualities": []string{"2160p"}, "cutoff": "2160p",
		"upgrades": true, "hdr": "require", "minMbPerMin": 20, "maxMbPerMin": 200,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := int64(created["id"].(float64))

	// invalid profiles bounce with a reason
	for _, bad := range []map[string]any{
		{"name": "", "qualities": []string{"1080p"}, "cutoff": "1080p", "hdr": "allow"},
		{"name": "x", "qualities": []string{"720p"}, "cutoff": "1080p", "hdr": "allow"},
		{"name": "x", "qualities": []string{"1080p"}, "cutoff": "1080p", "hdr": "allow", "minMbPerMin": 90, "maxMbPerMin": 40},
	} {
		if rec, _ := doJSON(t, h, "POST", "/api/v1/profiles", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("bad profile %v accepted: %d", bad, rec.Code)
		}
	}

	// update tightens the band
	rec, _ = doJSON(t, h, "PUT", "/api/v1/profiles/"+itoa(id), map[string]any{
		"name": "4K HDR", "qualities": []string{"2160p"}, "cutoff": "2160p",
		"upgrades": false, "hdr": "require", "minMbPerMin": 30, "maxMbPerMin": 150,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	rec, out = doJSON(t, h, "GET", "/api/v1/profiles", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	var listed struct {
		Profiles []quality.Profile `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	var got *quality.Profile
	for i := range listed.Profiles {
		if listed.Profiles[i].ID == id {
			got = &listed.Profiles[i]
		}
	}
	if got == nil || got.MaxMBPerMin != 150 || got.Upgrades {
		t.Fatalf("updated profile = %+v", got)
	}
	_ = out

	rec, _ = doJSON(t, h, "PUT", "/api/v1/profiles/9999", map[string]any{
		"name": "ghost", "qualities": []string{"1080p"}, "cutoff": "1080p", "hdr": "allow",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: %d, want 404", rec.Code)
	}

	// delete works down to — but never past — the last profile
	if rec, _ := doJSON(t, h, "DELETE", "/api/v1/profiles/"+itoa(id), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	lastID := int64(seeded[0].(map[string]any)["id"].(float64))
	if rec, _ := doJSON(t, h, "DELETE", "/api/v1/profiles/"+itoa(lastID), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("deleting the last profile: %d, want 400", rec.Code)
	}
}

func TestLibraryGetsDefaultProfileAndCanSwitch(t *testing.T) {
	h := testServer(t).Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	if lib["qualityProfileId"].(float64) == 0 {
		t.Fatal("new library did not inherit the default profile")
	}
	libID := int64(lib["id"].(float64))

	rec, created := doJSON(t, h, "POST", "/api/v1/profiles", map[string]any{
		"name": "720p only", "qualities": []string{"720p"}, "cutoff": "720p", "hdr": "allow",
	})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	profileID := int64(created["id"].(float64))

	rec, _ = doJSON(t, h, "PUT", "/api/v1/libraries/"+itoa(libID)+"/profile",
		map[string]any{"qualityProfileId": profileID})
	if rec.Code != http.StatusOK {
		t.Fatalf("set library profile: %d %s", rec.Code, rec.Body)
	}
	rec, out := doJSON(t, h, "GET", "/api/v1/libraries", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	libs := out["libraries"].([]any)
	if got := libs[0].(map[string]any)["qualityProfileId"].(float64); int64(got) != profileID {
		t.Fatalf("library profile = %v, want %d", got, profileID)
	}

	// pointing at a missing profile bounces
	rec, _ = doJSON(t, h, "PUT", "/api/v1/libraries/"+itoa(libID)+"/profile",
		map[string]any{"qualityProfileId": 9999})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad profile id: %d, want 400", rec.Code)
	}
}

// The profile is per title: a new movie inherits its library's default, a
// per-movie override survives the next metadata refresh.
func TestTitleProfileInheritsAndOverrides(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	libID := int64(lib["id"].(float64))
	defaultProfile := int64(lib["qualityProfileId"].(float64))
	movieID := seedMovie(t, srv, libID)

	var got struct {
		Movie struct {
			QualityProfileID int64 `json:"qualityProfileId"`
		} `json:"movie"`
	}
	read := func() int64 {
		rec, _ := doJSON(t, h, "GET", "/api/v1/movies/"+itoa(movieID), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("get movie: %d %s", rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got.Movie.QualityProfileID
	}
	if p := read(); p != defaultProfile {
		t.Fatalf("new movie profile = %d, want library default %d", p, defaultProfile)
	}

	rec, created := doJSON(t, h, "POST", "/api/v1/profiles", map[string]any{
		"name": "4K", "qualities": []string{"2160p"}, "cutoff": "2160p", "hdr": "allow",
	})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	override := int64(created["id"].(float64))

	rec, _ = doJSON(t, h, "PUT", "/api/v1/movies/"+itoa(movieID)+"/profile",
		map[string]any{"qualityProfileId": override})
	if rec.Code != http.StatusOK {
		t.Fatalf("set movie profile: %d %s", rec.Code, rec.Body)
	}
	if p := read(); p != override {
		t.Fatalf("override not stored: %d", p)
	}

	// a rescan refreshes metadata — the override must survive it
	seedMovie(t, srv, libID)
	if p := read(); p != override {
		t.Fatalf("override lost on refresh: %d, want %d", p, override)
	}

	// shows: same contract at show level
	rec, tvLib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": "/data/tv", "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	showID := seedShow(t, srv, int64(tvLib["id"].(float64)))
	rec, _ = doJSON(t, h, "PUT", "/api/v1/shows/"+itoa(showID)+"/profile",
		map[string]any{"qualityProfileId": override})
	if rec.Code != http.StatusOK {
		t.Fatalf("set show profile: %d %s", rec.Code, rec.Body)
	}
	var sh struct {
		Show struct {
			QualityProfileID int64 `json:"qualityProfileId"`
		} `json:"show"`
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/"+itoa(showID), nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &sh); err != nil {
		t.Fatal(err)
	}
	if sh.Show.QualityProfileID != override {
		t.Fatalf("show profile = %d, want %d", sh.Show.QualityProfileID, override)
	}
}
