// Package scene maps a show's catalog numbering onto the numbering its
// releases actually carry.
//
// TheTVDB's aired order is what reely stores and displays, and for most
// shows it is also what release groups use. It is not always: TVDB folds
// the 2007 and 2008 runs of "Kitchen Nightmares (US)" into one
// 22-episode Season 1, so every release from the 2010 season onward is
// numbered a season ahead of the catalog — the run TVDB calls S9 releases
// as S10. TheXEM is the community-maintained map between the two, keyed
// by TVDB series id, and it is the same map Sonarr searches with.
//
// The mapping is per EPISODE rather than per season, because the
// divergences are: an episode the scene splits in two, or a two-parter it
// merges, moves everything after it by one. A whole-season shift is just
// the common case of that.
package scene

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const xemBase = "https://thexem.info/map"

// haveMapTTL is how long the "which series are mapped at all" list is
// reused. It changes when somebody adds a show to XEM — daily is plenty,
// and it saves a per-show request for the large majority of libraries,
// which XEM has never heard of.
const haveMapTTL = 24 * time.Hour

// outageTTL is how long one failed request stands in for the rest. XEM
// being unreachable must not cost a call per show on every refresh pass,
// nor a log line per show: the first failure is reported, and for the
// window after it every caller is turned away without asking again.
const outageTTL = 5 * time.Minute

// ErrUnavailable reports that TheXEM was unreachable recently enough that
// this call did not bother asking. It is not a fact about any show, and
// callers must treat it as "no answer" rather than "no mapping": writing
// an empty result on it would erase numbering that is still correct and
// leave the show unfindable until the service came back.
var ErrUnavailable = errors.New("TheXEM unavailable")

// Mapping is one episode's two numberings.
type Mapping struct {
	Season, Episode           int // TheTVDB's numbers — what reely stores
	SceneSeason, SceneEpisode int // what a release of it carries
}

// XEM is a client for thexem.info. It needs no key and no configuration:
// the service is public, and a library whose shows it does not cover
// simply gets no mappings.
// defaultUserAgent identifies reely to TheXEM. It matters: the service
// sits behind Cloudflare, which refuses Go's default "Go-http-client/1.1"
// outright with a 403 — the same request from a browser succeeds. An
// honest application name is what the service's other clients send, and
// what it answers; impersonating a browser would be both dishonest and
// one Cloudflare rule away from breaking again.
const defaultUserAgent = "reely (+https://github.com/getreely/reely)"

type XEM struct {
	base   string
	ua     string
	client *http.Client

	mu       sync.Mutex
	have     map[int]bool
	haveAt   time.Time
	failedAt time.Time // when a request last failed, for outageTTL
}

func NewXEM() *XEM {
	return &XEM{base: xemBase, ua: defaultUserAgent, client: &http.Client{Timeout: 20 * time.Second}}
}

// SetUserAgent stamps the running version into the name reely gives
// TheXEM. Called once at startup; the default already identifies reely.
func (x *XEM) SetUserAgent(ua string) {
	if strings.TrimSpace(ua) != "" {
		x.ua = ua
	}
}

// SetBaseURL points the client somewhere else — tests aim it at a local
// fake; production never calls this.
func (x *XEM) SetBaseURL(u string) { x.base = u }

// envelope is XEM's response wrapper. data is decoded separately because
// a failure answers with a message and no usable data at all.
type envelope struct {
	Result  string          `json:"result"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// tolerated failure messages: XEM says these for a series it simply has
// no map for, which is the normal answer for most shows, not an error.
func tolerated(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "no show with the tvdb_id") ||
		strings.Contains(m, "no single connection")
}

func (x *XEM) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	u := x.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", x.ua)
	req.Header.Set("Accept", "application/json")
	resp, err := x.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// the body carries the reason a gateway refused — a Cloudflare
		// error code, a rate-limit notice — and a status alone sent us
		// looking in the wrong place once already
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		if reason := strings.TrimSpace(string(snippet)); reason != "" {
			return nil, fmt.Errorf("TheXEM %s: %s (%s)", path, resp.Status, reason)
		}
		return nil, fmt.Errorf("TheXEM %s: %s", path, resp.Status)
	}
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	if strings.EqualFold(env.Result, "failure") {
		if tolerated(env.Message) {
			return nil, nil
		}
		return nil, fmt.Errorf("TheXEM %s: %s", path, env.Message)
	}
	return env.Data, nil
}

// Mapped reports which TVDB series ids XEM has any map for. One request
// answers for the whole library, so a refresh pass can skip asking about
// shows nobody has ever mapped. The list is cached for a day; a failure
// to fetch it is reported, and the caller decides whether to ask per show
// anyway.
func (x *XEM) Mapped(ctx context.Context) (map[int]bool, error) {
	x.mu.Lock()
	switch {
	case time.Since(x.failedAt) < outageTTL:
		x.mu.Unlock()
		return nil, ErrUnavailable
	case x.have != nil && time.Since(x.haveAt) < haveMapTTL:
		have := x.have
		x.mu.Unlock()
		return have, nil
	}
	x.mu.Unlock()

	data, err := x.get(ctx, "/havemap", url.Values{"origin": {"tvdb"}})
	if err != nil {
		x.mu.Lock()
		x.failedAt = time.Now()
		x.mu.Unlock()
		return nil, err
	}
	// ids arrive as strings ("80552"), which is why this is not []int
	var ids []string
	if len(data) > 0 {
		if err := json.Unmarshal(data, &ids); err != nil {
			return nil, err
		}
	}
	have := make(map[int]bool, len(ids))
	for _, s := range ids {
		if id, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && id > 0 {
			have[id] = true
		}
	}
	x.mu.Lock()
	x.have, x.haveAt = have, time.Now()
	x.mu.Unlock()
	return have, nil
}

// Mappings fetches one series' episode map. A series XEM does not cover
// comes back empty with no error — that is the ordinary answer.
func (x *XEM) Mappings(ctx context.Context, tvdbID int) ([]Mapping, error) {
	if tvdbID <= 0 {
		return nil, nil
	}
	x.mu.Lock()
	down := time.Since(x.failedAt) < outageTTL
	x.mu.Unlock()
	if down {
		return nil, ErrUnavailable
	}
	data, err := x.get(ctx, "/all", url.Values{
		"origin": {"tvdb"}, "id": {strconv.Itoa(tvdbID)},
	})
	if err != nil {
		x.mu.Lock()
		x.failedAt = time.Now()
		x.mu.Unlock()
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var rows []struct {
		Scene struct {
			Season  int `json:"season"`
			Episode int `json:"episode"`
		} `json:"scene"`
		Tvdb struct {
			Season  int `json:"season"`
			Episode int `json:"episode"`
		} `json:"tvdb"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	out := make([]Mapping, 0, len(rows))
	for _, r := range rows {
		// specials are never mapped, and a row whose scene side is all
		// zeroes carries no numbering at all
		if r.Tvdb.Season == 0 || r.Tvdb.Episode == 0 {
			continue
		}
		if r.Scene.Season == 0 && r.Scene.Episode == 0 {
			continue
		}
		out = append(out, Mapping{
			Season: r.Tvdb.Season, Episode: r.Tvdb.Episode,
			SceneSeason: r.Scene.Season, SceneEpisode: r.Scene.Episode,
		})
	}
	return out, nil
}
