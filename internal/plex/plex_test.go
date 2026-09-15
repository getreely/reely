package plex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The sign-in flow against a fake plex.tv shaped like the real one — the
// payloads below are the fields the live endpoints actually returned.

func fakePlex(t *testing.T, claimed bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// every call has to identify the install, or plex.tv cannot tie a
		// poll back to the PIN that was created
		if r.Header.Get("X-Plex-Client-Identifier") == "" {
			t.Errorf("%s %s sent no client identifier", r.Method, r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("%s %s did not ask for JSON", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins":
			if r.URL.Query().Get("strong") != "true" {
				t.Error("PIN was not requested as strong")
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":238415531,"code":"xfqp3c1k337fyg",
				"authToken":null,"expiresIn":1800}`))
		case r.URL.Path == "/api/v2/pins/238415531":
			if claimed {
				_, _ = w.Write([]byte(`{"id":238415531,"code":"xfqp3c1k337fyg",
					"authToken":"tok-abc","expiresIn":1200}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":238415531,"code":"xfqp3c1k337fyg",
				"authToken":null,"expiresIn":1788}`))
		case r.URL.Path == "/api/v2/pins/999":
			http.Error(w, "not found", http.StatusNotFound)
		case r.URL.Path == "/api/v2/user":
			if r.Header.Get("X-Plex-Token") != "tok-abc" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"id":4021,"username":"jen","email":"jen@example.com",
				"thumb":"https://plex.tv/users/x/avatar"}`))
		case r.URL.Path == "/api/v2/resources":
			_, _ = w.Write([]byte(`[
				{"name":"living room","clientIdentifier":"srv-1111","provides":"server","owned":true},
				{"name":"phone","clientIdentifier":"bb11","provides":"client,player","owned":true},
				{"name":"a friend's","clientIdentifier":"cc22","provides":"server,player","owned":false}]`))
		default:
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testClient(t *testing.T, claimed bool) *Client {
	c := New("reely-test-install")
	c.SetBaseURL(fakePlex(t, claimed).URL)
	return c
}

// A new PIN comes back with somewhere to send the person, and the URL
// carries the same client id the poll will use — a mismatch is the one
// way this flow strands somebody on a PIN nothing can claim.
func TestNewPINCarriesTheClientIdentifier(t *testing.T) {
	c := testClient(t, false)
	pin, err := c.NewPIN(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pin.ID != 238415531 || pin.Code != "xfqp3c1k337fyg" {
		t.Fatalf("pin = %+v", pin)
	}
	if !strings.HasPrefix(pin.URL, "https://app.plex.tv/auth#?") {
		t.Errorf("url = %q, want plex.tv's approval page", pin.URL)
	}
	for _, want := range []string{"clientID=reely-test-install", "code=xfqp3c1k337fyg", "product"} {
		if !strings.Contains(pin.URL, want) {
			t.Errorf("url %q is missing %q", pin.URL, want)
		}
	}
	if pin.ExpiresIn != 1800 {
		t.Errorf("expiresIn = %d, want 1800", pin.ExpiresIn)
	}
}

// An unclaimed PIN is pending, not broken: the person is still on
// plex.tv, and the UI keeps waiting rather than showing an error.
func TestUnclaimedPINIsPending(t *testing.T) {
	c := testClient(t, false)
	if _, err := c.Token(context.Background(), 238415531); !errors.Is(err, ErrPending) {
		t.Fatalf("err = %v, want ErrPending", err)
	}
}

func TestClaimedPINYieldsATokenAndAccount(t *testing.T) {
	c := testClient(t, true)
	token, err := c.Token(context.Background(), 238415531)
	if err != nil {
		t.Fatal(err)
	}
	if token != "tok-abc" {
		t.Fatalf("token = %q", token)
	}
	acct, err := c.Account(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if acct.ID != 4021 || acct.Username != "jen" {
		t.Fatalf("account = %+v", acct)
	}
}

// plex.tv drops an expired PIN, and answers 404 for one created under
// another client identifier. Both mean start again.
func TestMissingPINReadsAsExpired(t *testing.T) {
	c := testClient(t, false)
	if _, err := c.Token(context.Background(), 999); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

// A wrong token is refused rather than quietly returning an empty
// account — matching on a zero id would collide every account into one.
func TestBadTokenIsRefused(t *testing.T) {
	c := testClient(t, true)
	if _, err := c.Account(context.Background(), "nope"); err == nil {
		t.Fatal("a bad token resolved to an account")
	}
}

// A Plex device provides several things at once, so servers are picked
// out by membership rather than equality — "server,player" is a server.
func TestServersPicksOutServersIncludingMultiRole(t *testing.T) {
	c := testClient(t, true)
	servers, err := c.Servers(context.Background(), "tok-abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("servers = %+v, want the two that provide 'server'", servers)
	}
	if servers[0].MachineID != "srv-1111" || !servers[0].Owned {
		t.Errorf("owned server = %+v", servers[0])
	}
	if servers[1].Owned {
		t.Errorf("a friend's server came back as owned: %+v", servers[1])
	}
}

// The account payload is matched on id and nothing else. This pins that
// down as a decoding fact: a response with the same email but a new id
// is a different account.
func TestAccountsAreDistinguishedByIDNotEmail(t *testing.T) {
	var a, b Account
	body := `{"id":4021,"username":"jen","email":"shared@example.com"}`
	other := `{"id":5150,"username":"jenny","email":"shared@example.com"}`
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(other), &b); err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("two accounts sharing an email collided on id")
	}
}
