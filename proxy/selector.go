package proxy

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/xeri/upvpn/provider"
)

// Selector aggregates every configured provider and picks one target per
// connection, tracking failures so a dead proxy is skipped for a while.
type Selector struct {
	providers   []provider.Provider
	strategy    string
	maxFails    int
	failTimeout time.Duration
	log         *slog.Logger

	mu        sync.Mutex
	entries   []*entry
	next      int
	location  string
	connected bool
}

type entry struct {
	target    provider.Target
	fails     int
	downUntil time.Time
}

// NewSelector builds a selector. Empty/invalid tuning falls back to sane
// defaults (round-robin, 1 failure, 30s cooldown).
func NewSelector(providers []provider.Provider, strategy string, maxFails int, failTimeout time.Duration, log *slog.Logger) *Selector {
	if strategy == "" {
		strategy = "round-robin"
	}
	if maxFails <= 0 {
		maxFails = 1
	}
	if failTimeout <= 0 {
		failTimeout = 30 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Selector{
		providers:   providers,
		strategy:    strategy,
		maxFails:    maxFails,
		failTimeout: failTimeout,
		log:         log,
	}
}

func key(t provider.Target) string {
	return t.Provider + "|" + t.Scheme + "|" + t.Addr()
}

// Refresh re-reads every provider and rebuilds the target list, preserving the
// health state of targets that are still present.
func (s *Selector) Refresh(ctx context.Context) {
	s.mu.Lock()
	health := make(map[string]entry, len(s.entries))
	for _, e := range s.entries {
		health[key(e.target)] = *e
	}
	s.mu.Unlock()

	var (
		targets   []provider.Target
		locations []string
		connected bool
	)
	for _, p := range s.providers {
		snap, err := p.Snapshot(ctx)
		if err != nil {
			s.log.Error("upvpn: provider unavailable", "err", err)
			continue
		}
		if snap.Connected {
			connected = true
		}
		if snap.Location != "" && !contains(locations, snap.Location) {
			locations = append(locations, snap.Location)
		}
		targets = append(targets, snap.Targets...)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]
	for _, t := range targets {
		e := &entry{target: t}
		if old, ok := health[key(t)]; ok {
			e.fails, e.downUntil = old.fails, old.downUntil
		}
		s.entries = append(s.entries, e)
	}
	s.next = 0
	s.location = strings.Join(locations, ",")
	s.connected = connected
}

// Snapshot returns the current aggregated view.
func (s *Selector) Snapshot() provider.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := provider.Snapshot{Connected: s.connected, Location: s.location}
	for _, e := range s.entries {
		snap.Targets = append(snap.Targets, e.target)
	}
	return snap
}

// Pick returns the next healthy target, round-robin or random. When every
// target is cooling down it returns the one that recovers first.
func (s *Selector) Pick() (provider.Target, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		return provider.Target{}, false
	}
	now := time.Now()
	healthy := make([]int, 0, len(s.entries))
	for i, e := range s.entries {
		if !now.Before(e.downUntil) {
			healthy = append(healthy, i)
		}
	}
	if len(healthy) == 0 {
		best := 0
		for i := range s.entries {
			if s.entries[i].downUntil.Before(s.entries[best].downUntil) {
				best = i
			}
		}
		return s.entries[best].target, true
	}
	var idx int
	if s.strategy == "random" {
		idx = healthy[rand.IntN(len(healthy))]
	} else {
		idx = healthy[0]
		for _, i := range healthy {
			if i >= s.next {
				idx = i
				break
			}
		}
		s.next = (idx + 1) % len(s.entries)
	}
	return s.entries[idx].target, true
}

// Report records the outcome of a request against t.
func (s *Selector) Report(t provider.Target, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if key(e.target) != key(t) {
			continue
		}
		if ok {
			e.fails, e.downUntil = 0, time.Time{}
			return
		}
		e.fails++
		if e.fails >= s.maxFails {
			e.downUntil = time.Now().Add(s.failTimeout)
			e.fails = 0
		}
		return
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
