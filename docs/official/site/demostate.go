package site

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/go-monolith/ghtmx/examples/crud"
)

// The crud demo mutates state, and this is a public site: every
// visitor gets their own store, keyed by a session cookie, so nobody
// edits what somebody else is looking at. The example itself keeps
// its simple single-store reference wiring — the isolation lives
// entirely in this mount via crud.StoreSelector.

const demoCookie = "ghtmx_demo"

// maxDemoSessions bounds total demo memory (each store is itself
// capped by the example); past it the stalest quarter is evicted.
const maxDemoSessions = 512

type demoSession struct {
	store    *crud.Store
	lastSeen time.Time
}

type demoSessionPool struct {
	mu       sync.Mutex
	sessions map[string]*demoSession
}

var demoState = &demoSessionPool{sessions: map[string]*demoSession{}}

func init() {
	crud.StoreSelector = func(r *http.Request) *crud.Store {
		return demoState.storeFor(demoSessionID(r))
	}
}

// demoSessionID reads the visitor cookie; requests without one (bare
// API calls, cookie-less clients) share the "" session.
func demoSessionID(r *http.Request) string {
	if c, err := r.Cookie(demoCookie); err == nil {
		return c.Value
	}
	return ""
}

func (p *demoSessionPool) storeFor(id string) *crud.Store {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	if !ok {
		if len(p.sessions) >= maxDemoSessions {
			p.evictStalestLocked()
		}
		s = &demoSession{store: crud.NewStore()}
		p.sessions[id] = s
	}
	s.lastSeen = time.Now()
	return s.store
}

// evictStalestLocked drops the least-recently-seen quarter of the
// sessions, amortizing eviction cost.
func (p *demoSessionPool) evictStalestLocked() {
	type entry struct {
		id   string
		seen time.Time
	}
	all := make([]entry, 0, len(p.sessions))
	for id, s := range p.sessions {
		all = append(all, entry{id, s.lastSeen})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seen.Before(all[j].seen) })
	for _, e := range all[:len(all)/4+1] {
		delete(p.sessions, e.id)
	}
}

// withDemoSession makes sure a demo request carries a session cookie,
// minting one for first-time visitors on both the response and the
// forwarded request.
func withDemoSession(w http.ResponseWriter, r *http.Request) *http.Request {
	if _, err := r.Cookie(demoCookie); err == nil {
		return r
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return r // degrade to the shared "" session
	}
	id := hex.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name:     demoCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	r.AddCookie(&http.Cookie{Name: demoCookie, Value: id})
	return r
}
