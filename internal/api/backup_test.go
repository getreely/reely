package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON %q: %v", w.Body, err)
	}
	return out
}

// Backups are admin-only end to end, and the restore handler stages the
// file and signals the restart hook.
func TestBackupEndpoints(t *testing.T) {
	srv := testServer(t)
	restarted := false
	srv.Restart = func() { restarted = true }
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

	do := func(cookie *http.Cookie, method, path string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		var payload []byte
		if body != nil {
			payload = mustJSON(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// plain users see nothing of any of it
	for _, probe := range []struct{ method, path string }{
		{"GET", "/api/v1/backups"}, {"POST", "/api/v1/backups"},
		{"GET", "/api/v1/backups/reely-20260101-000000.db"},
		{"DELETE", "/api/v1/backups/reely-20260101-000000.db"},
		{"POST", "/api/v1/restore"},
	} {
		if w := do(sam, probe.method, probe.path, map[string]any{}); w.Code != http.StatusForbidden {
			t.Errorf("%s %s as user = %d, want 403", probe.method, probe.path, w.Code)
		}
	}

	w = do(root, "POST", "/api/v1/backups", nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create backup: %d %s", w.Code, w.Body)
	}
	name := decodeMap(t, w)["name"].(string)
	if name == "" {
		t.Fatal("backup has no name")
	}

	w = do(root, "GET", "/api/v1/backups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list backups: %d", w.Code)
	}
	if list, ok := decodeMap(t, w)["backups"].([]any); !ok || len(list) != 1 {
		t.Fatalf("backups list wrong: %s", w.Body)
	}

	// download streams the file
	if w = do(root, "GET", "/api/v1/backups/"+name, nil); w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Fatalf("download: %d, %d bytes", w.Code, w.Body.Len())
	}

	// restore from the rotating backup stages and restarts
	w = do(root, "POST", "/api/v1/restore", map[string]any{"name": name})
	if w.Code != http.StatusOK || decodeMap(t, w)["staged"] != true {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if !restarted {
		t.Fatal("restore did not signal the restart hook")
	}

	if w = do(root, "DELETE", "/api/v1/backups/"+name, nil); w.Code != http.StatusOK {
		t.Fatalf("delete backup: %d", w.Code)
	}
	if w = do(root, "POST", "/api/v1/restore", map[string]any{"name": name}); w.Code != http.StatusBadRequest {
		t.Fatalf("restore of a deleted backup = %d, want 400", w.Code)
	}
}
