package api

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

var errTooManyAttempts = errors.New("too many sign-in attempts — wait a minute and try again")

// A bucket per caller for the routes that must be public.
//
// Password login has its own backoff in the auth store, keyed on the
// username being guessed. The Plex sign-in routes have no username to
// key on and no password to get wrong, so they need something coarser:
// a PIN request costs reely a round trip to plex.tv, and an unbounded
// one is a way to make this server hammer plex.tv on somebody's behalf.
//
// The limit is deliberately loose. A person signing in makes one PIN
// request and then polls every couple of seconds, so the ceiling has to
// clear an honest sign-in comfortably while still bounding a script.

type limiter struct {
	mu     sync.Mutex
	seen   map[string]*bucket
	perMin int
	swept  time.Time
}

type bucket struct {
	count int
	start time.Time
}

func newLimiter(perMin int) *limiter {
	return &limiter{seen: map[string]*bucket{}, perMin: perMin, swept: time.Now()}
}

// allow reports whether this caller may proceed, and records the attempt.
func (l *limiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// the map is swept rather than grown forever; a minute's worth of
	// callers is the most it ever holds
	if now.Sub(l.swept) > time.Minute {
		for k, b := range l.seen {
			if now.Sub(b.start) > time.Minute {
				delete(l.seen, k)
			}
		}
		l.swept = now
	}

	b := l.seen[key]
	if b == nil || now.Sub(b.start) > time.Minute {
		l.seen[key] = &bucket{count: 1, start: now}
		return true
	}
	b.count++
	return b.count <= l.perMin
}

// limit wraps a handler in a per-caller ceiling.
//
// The key is the peer address rather than a forwarded header: a header
// is set by whoever is talking to us, so trusting one lets a caller
// rotate their own key and defeat the limit entirely. Behind a reverse
// proxy that makes this a limit on the proxy, which is the honest
// reading — reely cannot tell one caller from another through it without
// being told which proxy to trust.
func limit(l *limiter, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			key = r.RemoteAddr
		}
		if !l.allow(key) {
			w.Header().Set("Retry-After", "60")
			writeErr(w, http.StatusTooManyRequests, errTooManyAttempts)
			return
		}
		h(w, r)
	}
}
