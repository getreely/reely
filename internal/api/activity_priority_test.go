package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Changing a download's priority forwards SAB's own scale (2 Force,
// 1 High, 0 Normal, -1 Low) to the queue API, and anything off that
// scale never leaves reely.
func TestActivityPriorityForwardsToSAB(t *testing.T) {
	srv := testServer(t)
	var gotValue, gotValue2 string
	sab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("mode") == "queue" && q.Get("name") == "priority" {
			gotValue, gotValue2 = q.Get("value"), q.Get("value2")
			_, _ = fmt.Fprint(w, `{"status": true}`)
			return
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer sab.Close()
	if err := srv.Settings.Set("sab_url", sab.URL); err != nil {
		t.Fatal(err)
	}
	if err := srv.Settings.Set("sab_api_key", "k"); err != nil {
		t.Fatal(err)
	}

	rec, body := doJSON(t, srv.Handler(), "POST", "/api/v1/activity/priority",
		map[string]any{"nzoIds": []string{"SABnzbd_nzo_x1"}, "priority": 2})
	if rec.Code != http.StatusOK || body["changed"] != float64(1) {
		t.Fatalf("priority change: %d %v", rec.Code, body)
	}
	if gotValue != "SABnzbd_nzo_x1" || gotValue2 != "2" {
		t.Fatalf("SAB asked to set %q to %q, want SABnzbd_nzo_x1 to 2", gotValue, gotValue2)
	}

	// off the scale → refused before SAB is ever asked
	rec, _ = doJSON(t, srv.Handler(), "POST", "/api/v1/activity/priority",
		map[string]any{"nzoIds": []string{"SABnzbd_nzo_x1"}, "priority": 5})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("priority 5: %d, want 400", rec.Code)
	}
	rec, _ = doJSON(t, srv.Handler(), "POST", "/api/v1/activity/priority",
		map[string]any{"nzoIds": []string{}, "priority": 1})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no ids: %d, want 400", rec.Code)
	}
}
