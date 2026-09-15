package sharing

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/plex"
)

// fakePlex stands in for both halves at once, because the split between
// them is the thing most likely to be got wrong: labels live on the
// media server, restrictions on plex.tv, and a restriction sent to the
// wrong one is accepted and ignored.
type fakePlex struct {
	mu      sync.Mutex
	labels  map[int64][]string
	filters map[int64][2]string // account id -> {movies, shows}
	account int64
	writes  int
	// reads counts per-item metadata GETs — the cost a settled library
	// must not keep paying every pass.
	reads int
	// deaf takes label writes and does nothing, still answering 200 —
	// which is a thing the real server does to a payload it cannot
	// parse, and the reason no write here is believed without a read
	// back.
	deaf bool
}

func newFakePlex(account int64) *fakePlex {
	return &fakePlex{
		labels:  map[int64][]string{},
		filters: map[int64][2]string{account: {"", ""}},
		account: account,
	}
}

func (f *fakePlex) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		p := r.URL.Path

		switch {
		// ---- plex.tv ----
		case strings.HasPrefix(p, "/api/friends/"):
			id, _ := strconv.ParseInt(strings.TrimPrefix(p, "/api/friends/"), 10, 64)
			f.filters[id] = [2]string{q.Get("filterMovies"), q.Get("filterTelevision")}
			f.writes++
			fmt.Fprint(w, `<MediaContainer/>`)

		case strings.Contains(p, "/shared_servers"):
			cur := f.filters[f.account]
			_, _ = fmt.Fprintf(w, `<MediaContainer><SharedServer id="1" userID="%d"
				username="watcher" email="w@example.test" allLibraries="0"
				acceptedAt="1700000000" filterMovies="%s" filterTelevision="%s">
				<Section id="10" key="1" title="Films" type="movie" shared="1"/>
				<Section id="11" key="2" title="Series" type="show" shared="1"/>
				</SharedServer></MediaContainer>`, f.account, cur[0], cur[1])

		// ---- media server ----
		case p == "/library/sections":
			fmt.Fprint(w, `<MediaContainer>
				<Directory key="1" title="Films" type="movie"><Location path="/films"/></Directory>
				<Directory key="2" title="Series" type="show"><Location path="/tv"/></Directory>
			</MediaContainer>`)

		case strings.HasPrefix(p, "/library/metadata/"):
			f.reads++
			key, _ := strconv.ParseInt(strings.TrimPrefix(p, "/library/metadata/"), 10, 64)
			var b strings.Builder
			b.WriteString(`<MediaContainer><Video ratingKey="` + strconv.FormatInt(key, 10) + `">`)
			for _, l := range f.labels[key] {
				b.WriteString(`<Label tag="` + l + `"/>`)
			}
			b.WriteString(`</Video></MediaContainer>`)
			fmt.Fprint(w, b.String())

		case r.Method == http.MethodPut:
			if f.deaf {
				_, _ = fmt.Fprint(w, `<MediaContainer/>`)
				return
			}
			key, _ := strconv.ParseInt(q.Get("id"), 10, 64)
			if rm := q.Get("label[].tag.tag-"); rm != "" {
				keep := []string{}
				for _, l := range f.labels[key] {
					if !strings.EqualFold(l, rm) {
						keep = append(keep, l)
					}
				}
				f.labels[key] = keep
			}
			for i := 0; ; i++ {
				v := q.Get(fmt.Sprintf("label[%d].tag.tag", i))
				if v == "" {
					break
				}
				dup := false
				for _, l := range f.labels[key] {
					dup = dup || strings.EqualFold(l, v)
				}
				if !dup {
					// Plex title-cases what it stores
					f.labels[key] = append(f.labels[key], strings.ToUpper(v[:1])+v[1:])
				}
			}
			fmt.Fprint(w, `<MediaContainer/>`)

		default: // section listing
			if strings.HasSuffix(p, "/all") && strings.Contains(p, "sections/1") {
				_, _ = fmt.Fprintf(w, `<MediaContainer><Video ratingKey="5427" title="A Film">
					<Guid id="tmdb://10096"/>%s</Video></MediaContainer>`, labelXML(f.labels[5427]))
				return
			}
			if strings.HasSuffix(p, "/all") {
				_, _ = fmt.Fprintf(w, `<MediaContainer><Directory ratingKey="17095" title="A Show">
					<Guid id="tvdb://778411"/>%s</Directory></MediaContainer>`, labelXML(f.labels[17095]))
				return
			}
			fmt.Fprint(w, `<MediaContainer/>`)
		}
	}
}

// harness wires a store and a reconciler at one fake serving both roles.
func harness(t *testing.T, account int64) (*sql.DB, *catalog.Store, *fakePlex, *Reconciler) {
	t.Helper()
	f := newFakePlex(account)
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)

	client := plex.New("reely-test")
	client.SetBaseURL(srv.URL) // the plex.tv half
	r := &Reconciler{Cat: cat, Plex: client, Cfg: Config{
		Token: "tok", MachineID: "m", ServerURL: srv.URL,
	}}
	return conn, cat, f, r
}

// watcher makes an account that signs in on one Plex identity and
// watches on another — the owner's shape, and the reason the share is
// not simply written against the sign-in account.
func watcher(t *testing.T, conn *sql.DB, cat *catalog.Store, name string, signin, watch int64) int64 {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO users (username, password_hash, role, plex_account_id)
		VALUES (?, 'x', 'user', ?)`, name, signin)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if err := cat.EnsurePersonalGroup(id, name); err != nil {
		t.Fatal(err)
	}
	if watch != 0 {
		if err := cat.SetShareAccount(id, watch); err != nil {
			t.Fatal(err)
		}
	}
	if err := cat.SetManaged(id, true); err != nil {
		t.Fatal(err)
	}
	return id
}

// The whole point, end to end: an entitlement becomes a label on the
// title and a restriction on the share.
func TestAPassProjectsEntitlementsOntoPlex(t *testing.T) {
	conn, cat, f, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	if err := cat.Grant(g.ID, "movie", 10096, 0, "A Film", 0); err != nil {
		t.Fatal(err)
	}

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if res.Labelled != 1 || res.Shares != 1 {
		t.Fatalf("labelled=%d shares=%d", res.Labelled, res.Shares)
	}
	if got := strings.ToLower(strings.Join(f.labels[5427], ",")); !strings.Contains(got, g.Label) {
		t.Fatalf("labels on the film = %v, want %q", f.labels[5427], g.Label)
	}
	// the restriction goes against the WATCHING account, not the sign-in one
	if _, wrote := f.filters[400]; wrote {
		t.Fatal("the restriction was written against the sign-in account")
	}
	if got := f.filters[500][0]; !strings.Contains(strings.ToLower(got), g.Label) {
		t.Fatalf("movie restriction = %q, want it to name %q", got, g.Label)
	}
}

// A title Plex has not scanned yet is not a failure. The entitlement is
// already real; the label waits.
func TestATitlePlexHasNotSeenIsPendingNotAnError(t *testing.T) {
	conn, cat, _, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	// nothing in the fake library carries this id
	if err := cat.Grant(g.ID, "movie", 999999, 0, "Not Scanned Yet", 0); err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending != 1 {
		t.Fatalf("pending = %d, want the unscanned title to be waiting", res.Pending)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("an unscanned title was reported as an error: %v", res.Errors)
	}
}

// A show may carry only a TVDB id.
func TestAShowIsLabelledOnItsTvdbIdAlone(t *testing.T) {
	conn, cat, f, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	if err := cat.Grant(g.ID, "show", 0, 778411, "A Show", 0); err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Labelled != 1 {
		t.Fatalf("labelled = %d (errors %v)", res.Labelled, res.Errors)
	}
	if got := strings.ToLower(strings.Join(f.labels[17095], ",")); !strings.Contains(got, g.Label) {
		t.Fatalf("show labels = %v", f.labels[17095])
	}
}

// Running twice must not write twice: Plex title-cases what it stores,
// so a pass that compared byte for byte would rewrite forever.
func TestASecondPassWritesNothing(t *testing.T) {
	conn, cat, f, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	if err := cat.Grant(g.ID, "movie", 10096, 0, "A Film", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	writes := f.writes
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.writes != writes {
		t.Fatalf("second pass rewrote the share: %d -> %d", writes, f.writes)
	}
	if res.Shares != 0 {
		t.Fatalf("second pass reported %d share writes", res.Shares)
	}
}

// Somebody nobody opted in is never touched, however many entitlements
// exist. Turning management on narrows what a person sees, and that is
// a decision, not a side effect.
func TestAnUnmanagedShareIsLeftAlone(t *testing.T) {
	conn, cat, f, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	if err := cat.SetManaged(uid, false); err != nil {
		t.Fatal(err)
	}
	g, _ := cat.DefaultGroup(uid)
	if err := cat.Grant(g.ID, "movie", 10096, 0, "A Film", 0); err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Shares != 0 || f.writes != 0 {
		t.Fatalf("an unmanaged share was written: shares=%d writes=%d", res.Shares, f.writes)
	}
	// the title is still labelled — labels harm nobody, restrictions do
	if res.Labelled != 1 {
		t.Fatalf("labelled = %d", res.Labelled)
	}
}

// A share edited by hand since reely last wrote it is reported rather
// than silently overwritten — but it is still corrected, because leaving
// it is how somebody quietly keeps access they were meant to lose.
func TestAHandEditedShareIsReported(t *testing.T) {
	conn, cat, f, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	if err := cat.Grant(g.ID, "movie", 10096, 0, "A Film", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.filters[500] = [2]string{"label=something_else", "label=something_else"}
	f.mu.Unlock()

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drifted) != 1 || res.Drifted[0] != "watcher" {
		t.Fatalf("drifted = %v, want the hand-edited share named", res.Drifted)
	}
	if got := strings.ToLower(f.filters[500][0]); !strings.Contains(got, g.Label) {
		t.Fatalf("the hand edit was left in place: %q", f.filters[500][0])
	}
}

// Order is not meaning: two restrictions naming the same labels restrict
// identically, and rewriting on order alone would churn forever.
func TestRestrictionsAreComparedByTheLabelsTheyName(t *testing.T) {
	if !sameRestriction("label=a%2Cb", "label=b%2Ca") {
		t.Fatal("same labels in a different order should compare equal")
	}
	if !sameRestriction("label=Reely.jo", "label=reely.jo") {
		t.Fatal("case should not matter — Plex title-cases what it stores")
	}
	if sameRestriction("label=a", "label=a%2Cb") {
		t.Fatal("a wider restriction should not compare equal to a narrower one")
	}
}

func labelXML(labels []string) string {
	var b strings.Builder
	for _, l := range labels {
		b.WriteString(`<Label tag="` + l + `"/>`)
	}
	return b.String()
}

// A settled library must not keep paying to confirm it is settled. The
// section listing already returns every item's labels, so a pass that
// changes nothing should touch no individual title — which on a library
// of thousands is the difference between two requests and thousands,
// every ten minutes, forever.
func TestASettledLibraryCostsNothingToConfirm(t *testing.T) {
	conn, cat, f, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	if err := cat.Grant(g.ID, "movie", 10096, 0, "A Film", 0); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.reads, f.writes = 0, 0
	f.mu.Unlock()

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	f.mu.Lock()
	reads, writes := f.reads, f.writes
	f.mu.Unlock()
	if reads != 0 || writes != 0 {
		t.Fatalf("a settled pass cost %d item reads and %d share writes, want none",
			reads, writes)
	}
	if res.Labelled != 0 {
		t.Fatalf("a settled pass reported %d labelled", res.Labelled)
	}
}

// A restriction written over a half-tagged library hides titles
// somebody is entitled to. Until it is written they still see what they
// saw before, so waiting for the next pass costs them nothing and
// writing early costs them access.
func TestAFailedLabelPassLeavesSharesAlone(t *testing.T) {
	conn, cat, f, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	if err := cat.Grant(g.ID, "movie", 10096, 0, "A Film", 0); err != nil {
		t.Fatal(err)
	}
	// the server takes the write and does nothing, which is a thing it
	// really does — so the label never lands
	f.mu.Lock()
	f.deaf = true
	f.mu.Unlock()

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Held {
		t.Fatal("a failed label pass still went on to write restrictions")
	}
	if res.Shares != 0 || f.writes != 0 {
		t.Fatalf("shares written on a half-tagged library: %d / %d writes",
			res.Shares, f.writes)
	}

	// and once labelling works, the pass completes normally
	f.mu.Lock()
	f.deaf = false
	f.mu.Unlock()
	res, err = r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Held || res.Shares != 1 {
		t.Fatalf("recovery pass: held=%v shares=%d errors=%v", res.Held, res.Shares, res.Errors)
	}
}

// A title Plex has not scanned yet is not a failure, and must not stop
// everybody else's restriction being written — otherwise one mid-flight
// download would freeze the whole install.
func TestATitleWaitingOnPlexDoesNotHoldSharesBack(t *testing.T) {
	conn, cat, f, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	if err := cat.Grant(g.ID, "movie", 10096, 0, "A Film", 0); err != nil {
		t.Fatal(err)
	}
	if err := cat.Grant(g.ID, "movie", 999999, 0, "Still Downloading", 0); err != nil {
		t.Fatal(err)
	}

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending != 1 {
		t.Fatalf("pending = %d, want the unscanned title waiting", res.Pending)
	}
	if res.Held {
		t.Fatal("a title waiting on Plex held every restriction back")
	}
	if res.Shares != 1 {
		t.Fatalf("shares = %d, want the restriction written anyway", res.Shares)
	}
	_ = f
}

// A count alone leaves the owner guessing which titles are waiting, and
// the answer is usually "the ones with no file yet" — worth being able
// to confirm rather than assume.
func TestWaitingTitlesAreNamed(t *testing.T) {
	conn, cat, _, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	if err := cat.Grant(g.ID, "movie", 10096, 0, "A Film", 0); err != nil {
		t.Fatal(err)
	}
	if err := cat.Grant(g.ID, "movie", 999999, 0, "Still Downloading", 0); err != nil {
		t.Fatal(err)
	}

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending != 1 {
		t.Fatalf("pending = %d", res.Pending)
	}
	if len(res.Waiting) != 1 || res.Waiting[0].Title != "Still Downloading" {
		t.Fatalf("waiting = %+v, want the title named", res.Waiting)
	}
	// nothing is on disk here, so nothing is a mismatch
	if res.Waiting[0].OnDisk || res.Mismatched != 0 {
		t.Fatalf("a title with no file was called a mismatch: %+v", res.Waiting)
	}
	// the one that landed is not in the list
	for _, w := range res.Waiting {
		if w.Title == "A Film" {
			t.Fatal("a labelled title was reported as waiting")
		}
	}
}

// The count stays exact when the list is capped: a thousand names would
// not help anybody recognise what is waiting.
func TestWaitingNamesAreCappedButCountedInFull(t *testing.T) {
	conn, cat, _, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)
	for i := range waitingNamed + 10 {
		if err := cat.Grant(g.ID, "movie", 900000+i, 0, "Missing", 0); err != nil {
			t.Fatal(err)
		}
	}
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending != waitingNamed+10 {
		t.Fatalf("pending = %d, want every waiting title counted", res.Pending)
	}
	if len(res.Waiting) != waitingNamed {
		t.Fatalf("named = %d, want the cap", len(res.Waiting))
	}
}

// The two reasons a title waits want opposite responses: one is a
// download to be patient about, the other is the media server holding
// that same file under a different title, which patience never fixes.
func TestAWaitingTitleSaysWhetherYouAlreadyHaveTheFile(t *testing.T) {
	conn, cat, _, r := harness(t, 500)
	uid := watcher(t, conn, cat, "jo", 400, 500)
	g, _ := cat.DefaultGroup(uid)

	lib, err := cat.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	// one reely holds a file for, which Plex knows as something else
	if _, err := conn.Exec(`INSERT INTO movies (tmdb_id, title, library_id, file_path)
		VALUES (555001, 'Matched Wrong', ?, '/films/matched wrong.mkv')`, lib.ID); err != nil {
		t.Fatal(err)
	}
	// and one that simply has not arrived
	if _, err := conn.Exec(`INSERT INTO movies (tmdb_id, title, library_id)
		VALUES (555002, 'Not Here Yet', ?)`, lib.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{555001, 555002} {
		title := "Matched Wrong"
		if id == 555002 {
			title = "Not Here Yet"
		}
		if err := cat.Grant(g.ID, "movie", id, 0, title, 0); err != nil {
			t.Fatal(err)
		}
	}

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending != 2 {
		t.Fatalf("pending = %d, want both waiting", res.Pending)
	}
	if res.Mismatched != 1 {
		t.Fatalf("mismatched = %d, want only the one reely holds a file for", res.Mismatched)
	}
	got := map[string]bool{}
	for _, w := range res.Waiting {
		got[w.Title] = w.OnDisk
	}
	if !got["Matched Wrong"] {
		t.Fatal("a title reely holds a file for was not flagged as a mismatch")
	}
	if got["Not Here Yet"] {
		t.Fatal("a title with no file was called a mismatch")
	}
}

// Pointing two managed accounts at one Plex account is a thing the
// "watches on" setting makes easy to do by accident: the owner names
// their watcher account, and that account already had a reely user of
// its own. Both would write the whole restriction for the same share,
// so what the person saw would be decided by whichever ran last, and
// would change between passes as the order did.
//
// Neither is written, and the pass says so. Applying one of two
// contradictory answers is worse than applying none, because nothing
// about the result would look wrong.
func TestTwoAccountsWatchingOnOneShareWriteNothing(t *testing.T) {
	conn, cat, f, r := harness(t, 500)
	// jo signs in as 400 and watches on 500
	jo := watcher(t, conn, cat, "jo", 400, 500)
	// sam signs in AS 500 — the same share jo was pointed at
	sam := watcher(t, conn, cat, "sam", 500, 0)
	g, _ := cat.DefaultGroup(jo)
	if err := cat.Grant(g.ID, "movie", 5427, 0, "A Film", 0); err != nil {
		t.Fatal(err)
	}

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Shares != 0 {
		t.Fatalf("shares written = %d, want none while two accounts claim one", res.Shares)
	}
	// the share exists in the fake from the start, so presence proves
	// nothing — what matters is that it is still empty
	if got := f.filters[500]; got[0] != "" || got[1] != "" {
		t.Fatalf("a contested share was written anyway: %v", got)
	}
	said := strings.Join(res.Errors, " ")
	if !strings.Contains(said, "500") {
		t.Fatalf("errors = %v, want the contested account named", res.Errors)
	}
	// once the collision is gone, the remaining one is written normally
	if err := cat.SetManaged(sam, false); err != nil {
		t.Fatal(err)
	}
	res, err = r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Shares != 1 {
		t.Fatalf("shares = %d after the clash was resolved: %v", res.Shares, res.Errors)
	}
	if got := f.filters[500][0]; !strings.Contains(strings.ToLower(got), g.Label) {
		t.Fatalf("restriction = %q, want jo's label", got)
	}
}
