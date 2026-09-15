package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestNamingTemplateRejectedBeforeItIsSaved(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	const good = `{Title} ({Year})/{Title} ({Year}) [{Quality}]`

	rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/naming_movie", map[string]string{"value": good})
	if rec.Code != http.StatusOK {
		t.Fatalf("valid template: %d %s", rec.Code, rec.Body)
	}

	// a template that would collide every movie onto one path
	rec, _ = doJSON(t, h, "PUT", "/api/v1/settings/naming_movie",
		map[string]string{"value": `Movies/{Year}`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("colliding template: %d, want 400", rec.Code)
	}
	// and the stored value is untouched — a rejected write must not be a
	// partial one, or the library reorganises around a template nobody chose
	if got := srv.Settings.Get("naming_movie"); got != good {
		t.Fatalf("stored template = %q after a rejected write", got)
	}
}

func TestNamingTemplateTrimmedOnSave(t *testing.T) {
	srv := testServer(t)
	rec, _ := doJSON(t, srv.Handler(), "PUT", "/api/v1/settings/naming_movie",
		map[string]string{"value": "  {Title} ({Year})  "})
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if got := srv.Settings.Get("naming_movie"); got != "{Title} ({Year})" {
		t.Fatalf("stored %q — surrounding space becomes a directory name", got)
	}
}

func TestNamingPreviewShowsPathAndErrorPerField(t *testing.T) {
	srv := testServer(t)
	rec, _ := doJSON(t, srv.Handler(), "POST", "/api/v1/settings/naming/preview", map[string]string{
		"movie": `{Title} ({Year})/{Title} [{Quality}]`,
		"show":  `{Title}/{Episode Title}`, // missing season and episode
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Movie  map[string]string   `json:"movie"`
		Show   map[string]string   `json:"show"`
		Tokens map[string][]string `json:"tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Movie["error"] != "" {
		t.Fatalf("valid movie template reported %q", out.Movie["error"])
	}
	if out.Movie["path"] != "Arrival (2016)/Arrival [1080p]" {
		t.Fatalf("movie preview = %q", out.Movie["path"])
	}
	// the bad show template must not suppress the good movie one
	if out.Show["error"] == "" {
		t.Fatal("a show template with no episode number previewed clean")
	}
	if len(out.Tokens["show"]) == 0 || len(out.Tokens["movie"]) == 0 {
		t.Fatalf("tokens = %+v — the UI builds its reference from these", out.Tokens)
	}
}
