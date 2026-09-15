package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/getreely/reely/internal/metadata"
)

// The request flow over HTTP, on ordinary local accounts — no Plex
// anywhere. That is the point of building it this way round: the whole
// feature can be exercised before a line of OAuth exists.

type testWorld struct {
	h       http.Handler
	srv     *Server
	admin   *http.Cookie
	adminID int64
	family  int64
	jen     int64
}

// requesterWorld builds an install with one admin, one shared library
// and a requester who may browse it but not add to it.
func requesterWorld(t *testing.T) *testWorld {
	t.Helper()
	srv := testServer(t)
	h := srv.Handler()

	rec, adminBody := doJSON(t, h, "POST", "/api/v1/users", map[string]any{
		"username": "root", "password": "correct horse battery"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create admin: %d %s", rec.Code, rec.Body)
	}
	adminID := int64(0)
	if v, ok := adminBody["id"].(float64); ok {
		adminID = int64(v)
	}
	if adminID == 0 {
		t.Fatalf("no admin id in %v", adminBody)
	}
	admin := login(t, h, "root", "correct horse battery")

	lib, err := srv.Catalog.CreateLibrary("Family", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	w := as(t, h, admin, "POST", "/api/v1/users", map[string]any{
		"username": "jen", "password": "another long one", "role": "user",
		"libraryIds": []int64{lib.ID},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create requester: %d %s", w.Code, w.Body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &created) //nolint:errcheck // asserted below
	if created.ID == 0 {
		t.Fatalf("no user id in %s", w.Body)
	}
	// a requester: browses the library, asks rather than adds
	if err := srv.Auth.SetRequestSettings(created.ID, false, false, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	return &testWorld{h: h, srv: srv, admin: admin, adminID: adminID,
		family: lib.ID, jen: created.ID}
}

// as performs a request as whoever the cookie belongs to.
func as(t *testing.T, h http.Handler, c *http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func dune() map[string]any {
	return map[string]any{
		"kind": "movie", "tmdbId": 693134, "title": "Dune: Part Two", "year": 2024,
	}
}

// A requester cannot add, and is told what to do instead.
func TestRequesterCannotAddDirectly(t *testing.T) {
	world := requesterWorld(t)
	jen := login(t, world.h, "jen", "another long one")

	w := as(t, world.h, jen, "POST", "/api/v1/movies", map[string]any{
		"tmdbId": 693134, "libraryId": world.family})
	if w.Code != http.StatusForbidden {
		t.Fatalf("requester adding directly = %d, want 403: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Request")) {
		t.Errorf("the refusal should point at requesting: %s", w.Body)
	}

	// An admin holds every library and so has no stored grants and no
	// default — there is nothing to pick between on their behalf. They
	// name where it goes, and it goes there.
	req := dune()
	req["libraryId"] = world.family
	if w := as(t, world.h, world.admin, "POST", "/api/v1/requests", req); w.Code != http.StatusCreated {
		t.Fatalf("admin request = %d: %s", w.Code, w.Body)
	}
	if w := as(t, world.h, world.admin, "POST", "/api/v1/movies", map[string]any{
		"tmdbId": 693134, "libraryId": world.family}); w.Code == http.StatusForbidden {
		t.Error("the may_add gate refused an admin")
	}
}

// One library means no question to ask: the request lands there without
// the caller naming it.
func TestRequestGoesToTheOnlyLibraryWithoutAsking(t *testing.T) {
	world := requesterWorld(t)
	jen := login(t, world.h, "jen", "another long one")

	w := as(t, world.h, jen, "POST", "/api/v1/requests", dune())
	if w.Code != http.StatusCreated {
		t.Fatalf("request = %d: %s", w.Code, w.Body)
	}
	open, err := world.srv.Catalog.OpenRequests([]int64{world.family})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].LibraryID != world.family {
		t.Fatalf("request landed on %+v, want the family library", open)
	}
}

// Everyone sharing a library shares its requests — and the listing names
// nobody, which is the rule the whole requester side is built on.
func TestRequestedIsSharedAndAnonymous(t *testing.T) {
	world := requesterWorld(t)
	jen := login(t, world.h, "jen", "another long one")

	if w := as(t, world.h, jen, "POST", "/api/v1/requests", dune()); w.Code != http.StatusCreated {
		t.Fatalf("first ask = %d: %s", w.Code, w.Body)
	}
	// asking again is a conflict, not a duplicate
	if w := as(t, world.h, jen, "POST", "/api/v1/requests", dune()); w.Code != http.StatusConflict {
		t.Errorf("second ask = %d, want 409: %s", w.Code, w.Body)
	}

	w := as(t, world.h, jen, "GET", "/api/v1/requests", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing = %d: %s", w.Code, w.Body)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("username")) ||
		bytes.Contains(w.Body.Bytes(), []byte("jen")) {
		t.Errorf("the requester's listing named who asked: %s", w.Body)
	}

	// the owner's queue is the one place a name appears
	q := as(t, world.h, world.admin, "GET", "/api/v1/requests/queue", nil)
	if !bytes.Contains(q.Body.Bytes(), []byte("jen")) {
		t.Errorf("the queue should name the asker: %s", q.Body)
	}
}

// Denying returns the title to askable, with no refused state to explain.
func TestDenyingReturnsTheTitleToAskable(t *testing.T) {
	world := requesterWorld(t)
	jen := login(t, world.h, "jen", "another long one")

	if w := as(t, world.h, jen, "POST", "/api/v1/requests", dune()); w.Code != http.StatusCreated {
		t.Fatalf("ask = %d: %s", w.Code, w.Body)
	}
	queued, err := world.srv.Catalog.QueuedRequests()
	if err != nil || len(queued) != 1 {
		t.Fatalf("queue = %v, %v", queued, err)
	}
	deny := as(t, world.h, world.admin, "POST",
		"/api/v1/requests/"+strconv.FormatInt(queued[0].ID, 10)+"/deny", nil)
	if deny.Code != http.StatusOK {
		t.Fatalf("deny = %d: %s", deny.Code, deny.Body)
	}
	// askable again
	if w := as(t, world.h, jen, "POST", "/api/v1/requests", dune()); w.Code != http.StatusCreated {
		t.Errorf("after denial = %d, want it askable again: %s", w.Code, w.Body)
	}
	// and a requester cannot decide anything
	if w := as(t, world.h, jen, "POST", "/api/v1/requests/1/deny", nil); w.Code != http.StatusForbidden {
		t.Errorf("requester denying = %d, want 403", w.Code)
	}
}

// The weekly limit refuses the ask over it, and says something a person
// can act on.
func TestQuotaRefusesOverTheWeeklyLimit(t *testing.T) {
	world := requesterWorld(t)
	one := 1
	if err := world.srv.Auth.SetRequestSettings(world.jen, false, false, false, &one, nil); err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")

	if w := as(t, world.h, jen, "POST", "/api/v1/requests", dune()); w.Code != http.StatusCreated {
		t.Fatalf("first ask = %d: %s", w.Code, w.Body)
	}
	second := map[string]any{"kind": "movie", "tmdbId": 872585, "title": "Oppenheimer"}
	w := as(t, world.h, jen, "POST", "/api/v1/requests", second)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over quota = %d, want 429: %s", w.Code, w.Body)
	}
	// shows are counted separately, so a film limit doesn't block one
	show := map[string]any{"kind": "show", "tmdbId": 1396, "title": "Breaking Bad"}
	if w := as(t, world.h, jen, "POST", "/api/v1/requests", show); w.Code == http.StatusTooManyRequests {
		t.Errorf("a film quota blocked a series: %s", w.Body)
	}
}

// The default library is the one thing an account writes about itself,
// and it may only name a library it already holds.
func TestDefaultLibraryIsOwnAndScoped(t *testing.T) {
	world := requesterWorld(t)
	other, err := world.srv.Catalog.CreateLibrary("Someone else", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")

	if w := as(t, world.h, jen, "PUT", "/api/v1/users/me/default-library",
		map[string]any{"libraryId": world.family}); w.Code != http.StatusOK {
		t.Fatalf("setting their own library = %d: %s", w.Code, w.Body)
	}
	// a library they don't hold reads as absent, not as forbidden
	if w := as(t, world.h, jen, "PUT", "/api/v1/users/me/default-library",
		map[string]any{"libraryId": other.ID}); w.Code != http.StatusNotFound {
		t.Errorf("a library they don't hold = %d, want 404: %s", w.Code, w.Body)
	}
	// and they cannot set anyone's request settings, including their own
	if w := as(t, world.h, jen, "PUT", "/api/v1/users/"+strconv.FormatInt(world.jen, 10)+"/requests",
		map[string]any{"mayAdd": true}); w.Code != http.StatusForbidden {
		t.Errorf("requester granting themselves may_add = %d, want 403", w.Code)
	}
}

// An admin holds every library, so when there is more than one a title
// could go to, they say which. Deliberate — not a consequence of admins
// having no stored grants, which would otherwise be quietly deciding it,
// and the stored default below is there to prove it stays that way.
//
// One library of the kind is the exception, covered separately: a choice
// with one option is not a choice.
func TestAdminMustChooseBetweenLibraries(t *testing.T) {
	world := requesterWorld(t)
	if _, err := world.srv.Catalog.CreateLibrary("Films 2", t.TempDir(), "movies"); err != nil {
		t.Fatal(err)
	}

	w := as(t, world.h, world.admin, "POST", "/api/v1/requests", dune())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("admin request with no library = %d, want 400: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("pick which library")) {
		t.Errorf("the refusal should say what to do: %s", w.Body)
	}

	// even with a default stored against them, they still name one
	if err := world.srv.Auth.SetUserLibraries(1, []int64{world.family}); err != nil {
		t.Fatal(err)
	}
	if err := world.srv.Auth.SetDefaultLibrary(1, world.family); err != nil {
		t.Fatal(err)
	}
	if w := as(t, world.h, world.admin, "POST", "/api/v1/requests", dune()); w.Code != http.StatusBadRequest {
		t.Errorf("admin with a stored default = %d, want 400 anyway: %s", w.Code, w.Body)
	}

	named := dune()
	named["libraryId"] = world.family
	if w := as(t, world.h, world.admin, "POST", "/api/v1/requests", named); w.Code != http.StatusCreated {
		t.Errorf("admin naming a library = %d: %s", w.Code, w.Body)
	}
}

// A title the install already holds anywhere needs no decision — the file
// is here, and putting it in another library hardlinks rather than
// downloads — so it is approved on the spot however the account is set.
func TestAlreadyHeldIsApprovedWithoutAsking(t *testing.T) {
	world := requesterWorld(t)
	// somebody else's library already carries it
	other, err := world.srv.Catalog.CreateLibrary("Mine", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := world.srv.Catalog.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 693134, Title: "Dune: Part Two"}, other.ID); err != nil {
		t.Fatal(err)
	}
	// and this account has a spent quota and no auto-approve, so nothing
	// but the "we already have it" rule can let this through
	zero := 0
	if err := world.srv.Auth.SetRequestSettings(world.jen, false, false, false, &zero, &zero); err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")

	w := as(t, world.h, jen, "POST", "/api/v1/requests", dune())
	if w.Code != http.StatusCreated {
		t.Fatalf("asking for a held title = %d, want 201: %s", w.Code, w.Body)
	}
	var got struct {
		Status string `json:"status"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if got.Status != "approved" {
		t.Errorf("status = %q, want approved: %s", got.Status, w.Body)
	}
	// and nothing lands in the owner's queue to decide
	queued, err := world.srv.Catalog.QueuedRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Errorf("queue = %+v, want nothing to decide", queued)
	}
}

// A series request carries the seasons it asked for, and approving it
// adds those rather than the whole show. The preview page puts season
// switches right next to the button, so honouring them on the add path
// and not the request path means somebody turns three seasons off, asks,
// and gets all of it.
func TestSeriesRequestKeepsItsSeasons(t *testing.T) {
	world := requesterWorld(t)
	shows, err := world.srv.Catalog.CreateLibrary("Series", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	if err := world.srv.Auth.SetUserLibraries(world.jen, []int64{world.family, shows.ID}); err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")

	w := as(t, world.h, jen, "POST", "/api/v1/requests", map[string]any{
		"kind": "show", "tmdbId": 1396, "title": "Breaking Bad",
		"libraryId": shows.ID, "seasons": []int{4, 5},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("asking for two seasons = %d: %s", w.Code, w.Body)
	}
	queued, err := world.srv.Catalog.QueuedRequests()
	if err != nil || len(queued) != 1 {
		t.Fatalf("queue = %v (%v)", queued, err)
	}
	if got := queued[0].Seasons; len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Errorf("seasons = %v, want [4 5] — the ask was for part of the show", got)
	}
}

// And a whole-show ask stays a whole-show ask: no seasons means all of
// them, which is what an untouched page means.
func TestWholeSeriesRequestNamesNoSeasons(t *testing.T) {
	world := requesterWorld(t)
	shows, err := world.srv.Catalog.CreateLibrary("Series", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	if err := world.srv.Auth.SetUserLibraries(world.jen, []int64{world.family, shows.ID}); err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")

	if w := as(t, world.h, jen, "POST", "/api/v1/requests", map[string]any{
		"kind": "show", "tmdbId": 1396, "title": "Breaking Bad", "libraryId": shows.ID,
	}); w.Code != http.StatusCreated {
		t.Fatalf("asking for a whole show = %d: %s", w.Code, w.Body)
	}
	queued, _ := world.srv.Catalog.QueuedRequests()
	if len(queued) != 1 || len(queued[0].Seasons) != 0 {
		t.Errorf("seasons = %v, want none — the whole show was asked for", queued)
	}
}

// A requester cannot add a NEW title, and that is the whole of what
// may_add withholds. It is deliberately narrow: somebody who holds a
// library still curates it, because deleting a title they were given or
// changing what is monitored acts on what is already theirs rather than
// adding to the install.
func TestRequesterCannotAddButStillCuratesTheirLibrary(t *testing.T) {
	world := requesterWorld(t)
	movie, err := world.srv.Catalog.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 693134, Title: "Dune: Part Two"}, world.family)
	if err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")

	// adding is refused, and says where to ask instead
	w := as(t, world.h, jen, "POST", "/api/v1/movies", map[string]any{
		"tmdbId": 872585, "libraryId": world.family})
	if w.Code != http.StatusForbidden {
		t.Fatalf("requester adding = %d, want 403: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Request")) {
		t.Errorf("the refusal should point at requesting: %s", w.Body)
	}

	// but the library they hold is theirs to manage
	path := "/api/v1/movies/" + strconv.FormatInt(movie, 10)
	if w := as(t, world.h, jen, "PUT", path+"/monitor",
		map[string]any{"monitored": false}); w.Code != http.StatusOK {
		t.Errorf("requester unmonitoring their own title = %d: %s", w.Code, w.Body)
	}
	if w := as(t, world.h, jen, "DELETE", path, nil); w.Code != http.StatusOK {
		t.Errorf("requester removing a title from their own library = %d: %s", w.Code, w.Body)
	}
}

// And library scope still binds: a requester reaches nothing in a
// library nobody gave them, however they ask for it.
func TestRequesterCannotTouchAnotherLibrary(t *testing.T) {
	world := requesterWorld(t)
	other, err := world.srv.Catalog.CreateLibrary("Someone else", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	movie, err := world.srv.Catalog.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 872585, Title: "Oppenheimer"}, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")

	path := "/api/v1/movies/" + strconv.FormatInt(movie, 10)
	// not found rather than forbidden: a library they cannot reach does
	// not exist as far as they are concerned
	if w := as(t, world.h, jen, "DELETE", path, nil); w.Code != http.StatusNotFound {
		t.Errorf("requester deleting from another library = %d, want 404: %s", w.Code, w.Body)
	}
	movies, err := world.srv.Catalog.ListMovies(other.ID)
	if err != nil || len(movies) != 1 {
		t.Fatalf("movies = %v (%v), want the other library untouched", movies, err)
	}
}

// An account that may add is unaffected.
func TestAddingAccountStillChangesTheLibrary(t *testing.T) {
	world := requesterWorld(t)
	if err := world.srv.Auth.SetRequestSettings(world.jen, true, false, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")
	if w := as(t, world.h, jen, "POST", "/api/v1/movies", map[string]any{
		"tmdbId": 693134, "libraryId": world.family}); w.Code == http.StatusForbidden {
		t.Errorf("the actor gate refused an account that may add: %s", w.Body)
	}
}

// An account that may add titles without asking does not queue its own
// requests. An admin asking would be filing into their own queue and
// then approving it, which is a step with one possible outcome — and it
// is the ordinary case on the portal, where nobody adds because the add
// routes are not served there.
func TestOwnAskNeedsNoApprovalForAnAccountThatMayAdd(t *testing.T) {
	world := requesterWorld(t)

	// the admin, asking from the portal where Add does not exist
	req := dune()
	req["libraryId"] = world.family
	w := as(t, world.h, world.admin, "POST", "/api/v1/requests", req)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin asking = %d: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"status":"approved"`)) {
		t.Errorf("admin's own ask = %s, want it approved on the spot", w.Body)
	}
	if queued, err := world.srv.Catalog.QueuedRequests(); err != nil || len(queued) != 0 {
		t.Errorf("queue = %d rows (%v), want the admin's own ask not waiting on them",
			len(queued), err)
	}

	// and a plain account trusted to add gets the same
	if err := world.srv.Auth.SetRequestSettings(world.jen, true, false, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")
	second := map[string]any{"kind": "movie", "tmdbId": 872585, "title": "Oppenheimer"}
	if w := as(t, world.h, jen, "POST", "/api/v1/requests", second); !bytes.Contains(
		w.Body.Bytes(), []byte(`"status":"approved"`)) {
		t.Errorf("an account that may add still queued its ask: %s", w.Body)
	}
}

// A requester still waits. That is the whole point of the queue, and it
// must not have been widened by the rule above.
func TestARequesterStillWaits(t *testing.T) {
	world := requesterWorld(t)
	jen := login(t, world.h, "jen", "another long one")

	w := as(t, world.h, jen, "POST", "/api/v1/requests", dune())
	if !bytes.Contains(w.Body.Bytes(), []byte(`"status":"pending"`)) {
		t.Fatalf("requester's ask = %s, want it waiting", w.Body)
	}
	queued, err := world.srv.Catalog.QueuedRequests()
	if err != nil || len(queued) != 1 {
		t.Errorf("queue = %d rows (%v), want the ask waiting on a decision", len(queued), err)
	}
}

// A quota bounds how often somebody sends the owner off to fetch
// something. An admin's ask is approved on the spot and sends nobody
// anywhere, so a limit there refuses a request that needed no
// permission — and a limit stored before a promotion would do exactly
// that, long after it stopped meaning anything.
func TestAnAdminIsNotHeldToAQuota(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	if _, err := world.srv.db.Exec(
		`UPDATE users SET quota_movies_week = 0 WHERE username = 'root'`); err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/requests", map[string]any{
		"kind": "movie", "tmdbId": 603, "title": "The Matrix",
		"libraryId": world.family,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("request = %d, want it through despite a stored quota: %s", w.Code, w.Body)
	}
	var out struct{ Status string }
	json.Unmarshal(w.Body.Bytes(), &out) //nolint:errcheck // asserted below
	if out.Status != "approved" {
		t.Fatalf("status = %q, want an admin's ask approved on the spot", out.Status)
	}
}

// A choice with one option is not a choice. An admin holds every
// library, so naming one is theirs to do — but not when there is only
// one it could possibly be, which is reely asking a question it already
// knows the answer to.
func TestAnAdminNeedNotNameTheOnlyLibraryOfThatKind(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	w := as(t, world.h, world.admin, "POST", "/api/v1/requests", map[string]any{
		"kind": "movie", "tmdbId": 603, "title": "The Matrix",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("request = %d, want the only films library chosen: %s", w.Code, w.Body)
	}

	// a second films library restores the question, and it is asked
	if _, err := world.srv.Catalog.CreateLibrary("Films 2", t.TempDir(), "movies"); err != nil {
		t.Fatal(err)
	}
	w = as(t, world.h, world.admin, "POST", "/api/v1/requests", map[string]any{
		"kind": "movie", "tmdbId": 604, "title": "The Matrix Reloaded",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("request = %d, want it to ask which library: %s", w.Code, w.Body)
	}
}

// Deciding is the owner's, and the owner is often not at home when
// somebody asks. The three decision routes are the only administrative
// ones served on the portal — and the level still has to hold there,
// because that surface is the internet-facing one.
func TestDecidingWorksFromThePortalForAnAdminAndNobodyElse(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	outside := world.srv.ExternalHandler()
	jen := login(t, world.h, "jen", "another long one")

	// a title the TMDB stub knows, so approving exercises the whole
	// decision and not just the routing
	ask := map[string]any{"kind": "movie", "tmdbId": 603, "title": "The Matrix"}
	if w := as(t, world.h, jen, "POST", "/api/v1/requests", ask); w.Code != http.StatusCreated {
		t.Fatalf("ask = %d: %s", w.Code, w.Body)
	}
	queued, err := world.srv.Catalog.QueuedRequests()
	if err != nil || len(queued) != 1 {
		t.Fatalf("queue = %v, %v", queued, err)
	}
	id := strconv.FormatInt(queued[0].ID, 10)

	// the owner, away from home, can see what is waiting
	if w := as(t, outside, world.admin, "GET", "/api/v1/requests/queue", nil); w.Code != http.StatusOK {
		t.Fatalf("queue from the portal = %d, want it served: %s", w.Code, w.Body)
	}
	// the asker cannot, however they reach it
	if w := as(t, outside, jen, "GET", "/api/v1/requests/queue", nil); w.Code == http.StatusOK {
		t.Fatalf("a requester read the queue from the portal: %s", w.Body)
	}
	if w := as(t, outside, jen, "POST", "/api/v1/requests/"+id+"/approve", nil); w.Code == http.StatusOK {
		t.Fatalf("a requester approved their own request from the portal: %s", w.Body)
	}

	// and the decision itself lands
	if w := as(t, outside, world.admin, "POST", "/api/v1/requests/"+id+"/approve", nil); w.Code != http.StatusOK {
		t.Fatalf("approve from the portal = %d: %s", w.Code, w.Body)
	}
}

// Widening the portal for those three must not have widened it for
// anything else. The rest of the administrative surface stays absent out
// there rather than refused, which is the property the external handler
// exists for.
func TestThePortalStillServesNoOtherAdminRoute(t *testing.T) {
	world := requesterWorld(t)
	outside := world.srv.ExternalHandler()

	// admin-level routes only — the portal legitimately serves plenty of
	// user-level ones, libraries among them
	for _, path := range []string{
		"/api/v1/users", "/api/v1/settings/naming_movie",
		"/api/v1/sharing/groups", "/api/v1/activity", "/api/v1/health",
		"/api/v1/backups", "/api/v1/formats",
	} {
		if w := as(t, outside, world.admin, "GET", path, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s from the portal = %d, want it absent: %s", path, w.Code, w.Body)
		}
	}
}

// Two readings of one listing. The wide one is what stops a second
// person asking for something already on its way, so it has to carry
// everybody's; a page about what I asked for wants only mine.
func TestTheRequestListingNarrowsToYourOwnWhenAsked(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	jen := login(t, world.h, "jen", "another long one")

	// a second requester sharing the same library
	w := as(t, world.h, world.admin, "POST", "/api/v1/users", map[string]any{
		"username": "sam", "password": "a third long one", "role": "user",
		"libraryIds": []int64{world.family},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create sam = %d: %s", w.Code, w.Body)
	}
	var made struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &made) //nolint:errcheck // asserted below
	if err := world.srv.Auth.SetRequestSettings(made.ID, false, false, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	sam := login(t, world.h, "sam", "a third long one")

	if w := as(t, world.h, jen, "POST", "/api/v1/requests", dune()); w.Code != http.StatusCreated {
		t.Fatalf("jen's ask = %d: %s", w.Code, w.Body)
	}
	if w := as(t, world.h, sam, "POST", "/api/v1/requests", map[string]any{
		"kind": "movie", "tmdbId": 603, "title": "The Matrix"}); w.Code != http.StatusCreated {
		t.Fatalf("sam's ask = %d: %s", w.Code, w.Body)
	}

	count := func(c *http.Cookie, path string) int {
		w := as(t, world.h, c, "GET", path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", path, w.Code, w.Body)
		}
		var out struct {
			Requests []struct{ Title string } `json:"requests"`
		}
		json.Unmarshal(w.Body.Bytes(), &out) //nolint:errcheck // asserted below
		return len(out.Requests)
	}

	// the wide listing carries both, which is what makes "already
	// requested" work for whoever looks next
	if n := count(jen, "/api/v1/requests"); n != 2 {
		t.Fatalf("wide listing = %d, want both asks", n)
	}
	// narrowed, each sees only their own
	if n := count(jen, "/api/v1/requests?mine=1"); n != 1 {
		t.Fatalf("jen's own = %d, want just hers", n)
	}
	if n := count(sam, "/api/v1/requests?mine=1"); n != 1 {
		t.Fatalf("sam's own = %d, want just his", n)
	}
}

// A requested show carries its TVDB id all the way to the owner's queue.
//
// With a TVDB key configured, a show search comes from TheTVDB, so a
// requested series routinely has a TVDB id and NO TMDB one. The queue
// row opens the title by whichever id it has — so if the id stopped
// making the round trip, the owner would be left unable to open a
// requested series to decide on it, which is the half where seeing the
// seasons matters most.
func TestAQueuedShowKeepsTheIdItWasAskedFor(t *testing.T) {
	world := requesterWorld(t)
	shows, err := world.srv.Catalog.CreateLibrary("Series", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	if err := world.srv.Auth.SetUserLibraries(world.jen,
		[]int64{world.family, shows.ID}); err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")

	// as a TVDB-sourced search result arrives: a tvdb id and no tmdb one
	w := as(t, world.h, jen, "POST", "/api/v1/requests", map[string]any{
		"kind": "show", "tvdbId": 305288, "title": "Stranger Things",
		"year": 2016, "libraryId": shows.ID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("request = %d: %s", w.Code, w.Body)
	}

	w = as(t, world.h, world.admin, "GET", "/api/v1/requests/queue", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("queue = %d: %s", w.Code, w.Body)
	}
	var out struct {
		Requests []struct {
			TmdbID int `json:"tmdbId"`
			TvdbID int `json:"tvdbId"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Requests) != 1 {
		t.Fatalf("queue holds %d requests, want 1", len(out.Requests))
	}
	if got := out.Requests[0]; got.TvdbID != 305288 {
		t.Errorf("queued request = tmdb %d tvdb %d, want the tvdb id it was asked for",
			got.TmdbID, got.TvdbID)
	}
}
