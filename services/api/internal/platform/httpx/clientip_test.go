package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveClientIP(t *testing.T) {
	trusted, err := ParseProxies("10.0.0.0/8, 192.0.2.7, 2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, peer string
		xff        []string
		want       string
	}{
		{"no proxy configured is covered below", "203.0.113.9:1", nil, "203.0.113.9"},
		{"untrusted peer cannot spoof", "203.0.113.9:1", []string{"198.51.100.1"}, "203.0.113.9"},
		{"trusted proxy: client from header", "10.1.2.3:443", []string{"198.51.100.1"}, "198.51.100.1"},
		{"read from the right, client-supplied prefix ignored", "10.1.2.3:443", []string{"6.6.6.6, 198.51.100.1, 10.9.9.9"}, "198.51.100.1"},
		{"several header lines", "10.1.2.3:443", []string{"6.6.6.6", "198.51.100.1"}, "198.51.100.1"},
		{"exact-address proxy", "192.0.2.7:1", []string{"198.51.100.2"}, "198.51.100.2"},
		{"ipv6 proxy and client", "[2001:db8::1]:443", []string{"2001:db9::5"}, "2001:db9::5"},
		{"ipv4-mapped peer", "[::ffff:10.1.2.3]:443", []string{"198.51.100.1"}, "198.51.100.1"},
		{"trusted peer without header", "10.1.2.3:443", nil, "10.1.2.3"},
		{"malformed header: key on the proxy", "10.1.2.3:443", []string{"not-an-ip"}, "10.1.2.3"},
		{"only proxies in the chain", "10.1.2.3:443", []string{"10.5.5.5"}, "10.1.2.3"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := resolveClientIP(r, trusted); got != c.want {
			t.Errorf("%s: want %s, got %s", c.name, c.want, got)
		}
	}
	// With no trusted proxies the header is never used.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.2.3:1"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := resolveClientIP(r, nil); got != "10.1.2.3" {
		t.Errorf("no trusted proxies: got %s", got)
	}
}

func TestClientIPsMiddlewareAndDefault(t *testing.T) {
	trusted, _ := ParseProxies("10.0.0.1")
	var seen string
	h := ClientIPs(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = ClientIP(r) }))
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:9"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "198.51.100.7" {
		t.Errorf("middleware: %s", seen)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.4:5"
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	if got := ClientIP(r); got != "203.0.113.4" {
		t.Errorf("default without middleware must be the peer, got %s", got)
	}
}

func TestParseProxiesRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"nope", "10.0.0.0/33", "10.0.0.1, x"} {
		if _, err := ParseProxies(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if p, err := ParseProxies(" "); err != nil || len(p) != 0 {
		t.Errorf("blank list: %v %v", p, err)
	}
}
