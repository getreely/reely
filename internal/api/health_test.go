package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeHealthProwlarr serves the three health endpoints with one indexer
// happy and one benched.
func fakeHealthProwlarr(t *testing.T) *httptest.Server {
	t.Helper()
	benchedTill := time.Now().Add(30 * time.Minute).Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/indexer":
			_, _ = fmt.Fprint(w, `[{"id":1,"name":"NZBgeek","enable":true},
				{"id":2,"name":"DrunkenSlug","enable":true},
				{"id":3,"name":"Retired","enable":false}]`)
		case "/api/v1/indexerstatus":
			_, _ = fmt.Fprintf(w, `[{"indexerId":2,"disabledTill":%q,"mostRecentFailure":%q}]`,
				benchedTill, time.Now().Format(time.RFC3339))
		case "/api/v1/health":
			_, _ = fmt.Fprint(w, `[{"source":"IndexerStatusCheck","type":"warning",
				"message":"Indexers unavailable due to failures: DrunkenSlug"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakeHealthSab(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"queue":{"version":"4.3.2","paused":true,"diskspace1":"512.75","slots":[]}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type healthResp struct {
	Tmdb struct {
		Configured bool `json:"configured"`
	} `json:"tmdb"`
	Prowlarr struct {
		Configured bool   `json:"configured"`
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		Indexers   []struct {
			Name         string `json:"name"`
			Enabled      bool   `json:"enabled"`
			Healthy      bool   `json:"healthy"`
			DisabledTill string `json:"disabledTill"`
		} `json:"indexers"`
		Warnings []string `json:"warnings"`
	} `json:"prowlarr"`
	Sab struct {
		Configured bool    `json:"configured"`
		OK         bool    `json:"ok"`
		Version    string  `json:"version"`
		Paused     bool    `json:"paused"`
		DiskFreeGB float64 `json:"diskFreeGb"`
	} `json:"sab"`
}

func TestHealthReportsIndexersAndSab(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()

	// unconfigured: everything reports not set up, nothing errors
	rec, _ := doJSON(t, h, "GET", "/api/v1/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health unconfigured: %d", rec.Code)
	}
	var out healthResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Prowlarr.Configured || out.Sab.Configured || out.Tmdb.Configured {
		t.Fatalf("unconfigured install reports configured: %+v", out)
	}

	for k, v := range map[string]string{
		"prowlarr_url": fakeHealthProwlarr(t).URL, "prowlarr_api_key": "pk",
		"sab_url": fakeHealthSab(t).URL, "sab_api_key": "sk",
	} {
		if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/"+k, map[string]string{"value": v}); rec.Code != http.StatusOK {
			t.Fatal(rec.Body)
		}
	}

	rec, _ = doJSON(t, h, "GET", "/api/v1/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health: %d %s", rec.Code, rec.Body)
	}
	out = healthResp{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Prowlarr.OK || len(out.Prowlarr.Indexers) != 3 {
		t.Fatalf("prowlarr = %+v", out.Prowlarr)
	}
	byName := map[string]int{}
	for i, ix := range out.Prowlarr.Indexers {
		byName[ix.Name] = i
	}
	if ix := out.Prowlarr.Indexers[byName["NZBgeek"]]; !ix.Healthy || !ix.Enabled {
		t.Fatalf("healthy indexer misreported: %+v", ix)
	}
	if ix := out.Prowlarr.Indexers[byName["DrunkenSlug"]]; ix.Healthy || ix.DisabledTill == "" {
		t.Fatalf("benched indexer misreported: %+v", ix)
	}
	if ix := out.Prowlarr.Indexers[byName["Retired"]]; ix.Healthy || ix.Enabled {
		t.Fatalf("disabled indexer misreported: %+v", ix)
	}
	if len(out.Prowlarr.Warnings) != 1 {
		t.Fatalf("warnings = %v", out.Prowlarr.Warnings)
	}
	if !out.Sab.OK || out.Sab.Version != "4.3.2" || !out.Sab.Paused || out.Sab.DiskFreeGB != 512.75 {
		t.Fatalf("sab = %+v", out.Sab)
	}
}

func TestHealthIsAdminOnly(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	if rec, _ := doJSON(t, h, "POST", "/api/v1/users",
		map[string]any{"username": "root", "password": "correct horse battery"}); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	root := login(t, h, "root", "correct horse battery")

	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader(mustJSON(map[string]any{
		"username": "sam", "password": "another passphrase", "role": "user",
		"libraryIds": []any{lib["id"]},
	})))
	req.AddCookie(root)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body)
	}
	sam := login(t, h, "sam", "another passphrase")

	req = httptest.NewRequest("GET", "/api/v1/health", nil)
	req.AddCookie(sam)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("plain user reads health: %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/v1/health", nil)
	req.AddCookie(root)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin health: %d %s", w.Code, w.Body)
	}
}
