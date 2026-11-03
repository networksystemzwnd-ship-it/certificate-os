package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// Counter is a shared fixed-window counter (PostgreSQL in production). Hit counts one attempt for key in the
// current window of the given length and returns the number of attempts so far in that window and how long
// until the window ends.
type Counter interface {
	Hit(ctx context.Context, key string, window time.Duration) (count int, resetIn time.Duration, err error)
}

// Limiter is a fixed-window rate limiter. By default it is in memory and per process. Use attaches a shared
// Counter so the limit holds across API replicas; if the shared counter fails, the in-memory window decides
// (the limiter fails open to a per-process limit rather than blocking logins because a counter is down).
type Limiter struct {
	shared Counter
	name   string
	onErr  func(error)

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
	if l.shared != nil {
		sum := sha256.Sum256([]byte(key))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		n, reset, err := l.shared.Hit(ctx, l.name+":"+hex.EncodeToString(sum[:]), l.window)
		cancel()
		if err == nil {
			if n > l.limit {
				return false, reset
			}
			return true, 0
		}
		if l.onErr != nil {
			l.onErr(err)
		}
	}
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

// Use attaches a shared counter. name separates this limiter's keys from the others'; the subject (an IP
// address or email) is hashed before it leaves the process. onErr, if set, is told about counter failures.
func (l *Limiter) Use(name string, c Counter, onErr func(error)) *Limiter {
	l.shared, l.name, l.onErr = c, name, onErr
	return l
}
