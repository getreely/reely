package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/settings"
)

// qBittorrent has no API key: the WebUI password is the credential. So
// it has to be treated as one — sealed at rest and never echoed back —
// the same as SAB's key, rather than kept in the clear because it
// happens to be called a password.
func TestQbitPasswordIsTreatedAsACredential(t *testing.T) {
	h := testServer(t).Handler()

	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/qbit_password",
		map[string]string{"value": "hunter2"}); rec.Code != http.StatusOK {
		t.Fatalf("saving the password: %d %s", rec.Code, rec.Body)
	}

	// the API says whether one is set, never what it is
	rec, _ := doJSON(t, h, "GET", "/api/v1/settings/qbit_password", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading it back: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("the password was echoed back: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"set":true`) {
		t.Errorf("the card cannot tell that a password is set: %s", rec.Body)
	}

	// and it is registered as a credential, which is the one flag that
	// drives both behaviours: the masking above, and sealing at rest.
	// The sealing itself is the settings package's job and only happens
	// with a keeper wired, which production does and tests do not — so
	// the flag is what there is to assert here.
	if !settings.IsSecret("qbit_password") {
		t.Error("qbit_password is not registered as a credential, so it will be stored in the clear")
	}

	// the username is not a credential and reads back normally, because
	// the card shows it in a plain field
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/qbit_username",
		map[string]string{"value": "admin"}); rec.Code != http.StatusOK {
		t.Fatalf("saving the username: %d %s", rec.Code, rec.Body)
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/settings/qbit_username", nil)
	if !strings.Contains(rec.Body.String(), "admin") {
		t.Errorf("the username should read back so the field can show it: %s", rec.Body)
	}
}

// A URL on its own is a complete qBittorrent setup: it can be told to
// bypass authentication for localhost or a whitelisted subnet, which is
// an ordinary LAN arrangement. Requiring a password would be reely
// inventing a rule qBittorrent does not have.
func TestQbitNeedsOnlyAURL(t *testing.T) {
	srv := testServer(t)
	if srv.Qbit.Configured() {
		t.Fatal("configured before anything was set")
	}
	if err := srv.Settings.Set("qbit_url", "http://qbittorrent:8080"); err != nil {
		t.Fatal(err)
	}
	if !srv.Qbit.Configured() {
		t.Error("a URL with no credentials should be a working configuration")
	}
}

// The health panel reports the two download clients apart. An install
// may run both, and "the download client is unwell" does not say which.
func TestHealthReportsTheClientsSeparately(t *testing.T) {
	srv := testServer(t)
	rec, _ := doJSON(t, srv.Handler(), "GET", "/api/v1/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health: %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, key := range []string{`"sab"`, `"qbit"`} {
		if !strings.Contains(body, key) {
			t.Errorf("health does not report %s: %s", key, body)
		}
	}
	// an unconfigured client must read as "not set up" rather than as a
	// failure — most installs will only ever run one of the two
	if !strings.Contains(body, `"qbit":{"configured":false`) {
		t.Errorf("an unconfigured qBittorrent should report itself as such: %s", body)
	}
}
