package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/backup"
	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/grab"
	"github.com/getreely/reely/internal/importer"
	"github.com/getreely/reely/internal/lists"
	"github.com/getreely/reely/internal/mdblist"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/prowlarr"
	"github.com/getreely/reely/internal/qbittorrent"
	"github.com/getreely/reely/internal/sabnzbd"
	"github.com/getreely/reely/internal/settings"
	"github.com/getreely/reely/internal/trakt"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	cfg := settings.New(conn)
	cat := catalog.New(conn)
	tmdb := metadata.NewTMDB(func() string { return cfg.Get("tmdb_api_key") })
	// same construction as main: the indexer reads settings live, so a test
	// can point prowlarr_url at a fake and the handler follows
	indexer := prowlarr.New(
		func() string { return cfg.Get("prowlarr_url") },
		func() string { return cfg.Get("prowlarr_api_key") })
	sab := sabnzbd.New(
		func() string { return cfg.Get("sab_url") },
		func() string { return cfg.Get("sab_api_key") })
	qbit := qbittorrent.New(
		func() string { return cfg.Get("qbit_url") },
		func() string { return cfg.Get("qbit_username") },
		func() string { return cfg.Get("qbit_password") })
	grabber := &grab.Service{Indexer: indexer, Usenet: sab, Torrent: qbit, Catalog: cat}
	tvdb := metadata.NewTVDB(func() string { return cfg.Get("tvdb_api_key") })
	if base := cfg.Get("tvdb_test_base"); base != "" {
		tvdb.SetBaseURL(base)
	}
	traktClient := trakt.New(func() string { return cfg.Get("trakt_client_id") })
	srv, err := New(Deps{
		DB: conn, Version: "test",
		Dist:     fstest.MapFS{"dist/index.html": {Data: []byte("<html>reely</html>")}},
		Catalog:  cat,
		TMDB:     tmdb,
		TVDB:     tvdb,
		Importer: importer.New(cat, tmdb, tvdb),
		Settings: cfg,
		Auth:     auth.New(conn),
		Grab:     grabber,
		Prowlarr: indexer,
		Sab:      sab,
		Qbit:     qbit,
		Trakt:    traktClient,
		Lists: &lists.Syncer{
			Catalog: cat, TMDB: tmdb, TVDB: tvdb, Trakt: traktClient, Grab: grabber, Settings: cfg,
			Mdblist: mdblist.New(),
		},
		Backup: &backup.Service{DB: conn, DBPath: filepath.Join(dir, "reely.db"), Dir: filepath.Join(dir, "backups")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &out) //nolint:errcheck,gosec // some responses aren't objects
	return rec, out
}

func login(t *testing.T, h http.Handler, username, password string) *http.Cookie {
	t.Helper()
	rec, _ := doJSON(t, h, "POST", "/api/v1/auth/login",
		map[string]string{"username": username, "password": password})
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("login %s set no cookie", username)
	}
	return cookies[0]
}

func TestLibraryLifecycle(t *testing.T) {
	h := testServer(t).Handler()

	rec, out := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create library: %d %s", rec.Code, rec.Body)
	}
	if out["kind"] != "movies" {
		t.Fatalf("library kind = %v", out["kind"])
	}

	rec, _ = doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Bad", "path": "/data/x", "kind": "music"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind accepted: %d", rec.Code)
	}

	rec, out = doJSON(t, h, "GET", "/api/v1/libraries", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	if libs, _ := out["libraries"].([]any); len(libs) != 1 {
		t.Fatalf("libraries = %v", out)
	}
}

func TestScopedUserSeesOnlyTheirLibraries(t *testing.T) {
	h := testServer(t).Handler()
	rec, movies := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	if rec, _ := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Shows", "path": "/data/tv", "kind": "shows"}); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	if rec, _ := doJSON(t, h, "POST", "/api/v1/users",
		map[string]any{"username": "root", "password": "correct horse battery"}); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	root := login(t, h, "root", "correct horse battery")

	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader(mustJSON(map[string]any{
		"username": "sam", "password": "another passphrase", "role": "user",
		"libraryIds": []any{movies["id"]},
	})))
	req.AddCookie(root)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create scoped user: %d %s", w.Code, w.Body)
	}
	sam := login(t, h, "sam", "another passphrase")

	req = httptest.NewRequest("GET", "/api/v1/libraries", nil)
	req.AddCookie(sam)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out struct {
		Libraries []catalog.Library `json:"libraries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Libraries) != 1 || out.Libraries[0].Name != "Movies" {
		t.Fatalf("scoped user sees %v", out.Libraries)
	}
}

func TestSecretSettingNeverEchoes(t *testing.T) {
	h := testServer(t).Handler()
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/tmdb_api_key",
		map[string]string{"value": "tmdb-test-value"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	rec, out := doJSON(t, h, "GET", "/api/v1/settings/tmdb_api_key", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if _, leaked := out["value"]; leaked {
		t.Error("secret setting echoed its value")
	}
	if out["set"] != true {
		t.Errorf("set = %v, want true", out["set"])
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
