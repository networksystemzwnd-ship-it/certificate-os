package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/auth"
)

// fakeCounter is a shared counter that two limiters (two "replicas") use.
type fakeCounter struct {
	mu    sync.Mutex
	hits  map[string]int
	err   error
	reset time.Duration
}

func (f *fakeCounter) Hit(_ context.Context, key string, _ time.Duration) (int, time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, 0, f.err
	}
	f.hits[key]++
	return f.hits[key], f.reset, nil
}

func TestSharedLimiterHoldsAcrossReplicas(t *testing.T) {
	c := &fakeCounter{hits: map[string]int{}, reset: 42 * time.Second}
	a := auth.NewLimiter(3, time.Minute).Use("login_ip", c, nil)
	b := auth.NewLimiter(3, time.Minute).Use("login_ip", c, nil)
	for i, l := range []*auth.Limiter{a, b, a} {
		if ok, _ := l.Allow("203.0.113.9"); !ok {
			t.Fatalf("attempt %d must be allowed", i+1)
		}
	}
	// The 4th attempt lands on replica B but counts against the same shared window.
	ok, retry := b.Allow("203.0.113.9")
	if ok || retry != 42*time.Second {
		t.Fatalf("4th attempt must be refused with the counter's reset time, got %v %v", ok, retry)
	}
	if ok, _ := b.Allow("198.51.100.1"); !ok {
		t.Error("another subject has its own window")
	}
	// Keys are namespaced per limiter and the subject never leaves the process in clear.
	for k := range c.hits {
		if !strings.HasPrefix(k, "login_ip:") || strings.Contains(k, "203.0.113") || strings.Contains(k, "198.51") {
			t.Errorf("unexpected shared key %q", k)
		}
	}
}

func TestSharedLimiterFallsBackToMemoryWhenTheCounterFails(t *testing.T) {
	c := &fakeCounter{hits: map[string]int{}, err: errors.New("db down")}
	var reported int
	l := auth.NewLimiter(2, time.Minute).Use("x", c, func(error) { reported++ })
	l.Allow("k")
	l.Allow("k")
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("the in-memory window must still limit while the counter is down")
	}
	if reported != 3 {
		t.Errorf("each failure must be reported, got %d", reported)
	}
}
