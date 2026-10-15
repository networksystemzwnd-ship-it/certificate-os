package auth

import (
	"sync"
	"time"
)

// Limiter is a fixed-window in-memory rate limiter. It is per-process; a shared limiter
// (Postgres/Redis) is needed before running multiple API replicas — see DECISIONS D-108.
type Limiter struct {
	mu     sync.Mutex
	now    func() time.Time
	limit  int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	start time.Time
	count int
}

func NewLimiter(limit int, per time.Duration) *Limiter {
	return &Limiter{now: time.Now, limit: limit, window: per, hits: map[string]*window{}}
}

// Allow records an attempt for key and reports whether it is within the limit.
// When denied it returns how long until the window resets.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.hits) > 10_000 { // bound memory: sweep expired windows
		for k, w := range l.hits {
			if now.Sub(w.start) >= l.window {
				delete(l.hits, k)
			}
		}
	}
	w := l.hits[key]
	if w == nil || now.Sub(w.start) >= l.window {
		l.hits[key] = &window{start: now, count: 1}
		return true, 0
	}
	if w.count >= l.limit {
		return false, w.start.Add(l.window).Sub(now)
	}
	w.count++
	return true, 0
}

// SetClock replaces the time source (tests only).
func (l *Limiter) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}
