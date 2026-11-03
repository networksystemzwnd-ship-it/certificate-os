package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientIPKey struct{}

// ParseProxies parses a comma-separated list of IP addresses and CIDR ranges ("10.0.0.0/8, 192.0.2.7").
func ParseProxies(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or CIDR range", part)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func inAny(a netip.Addr, nets []netip.Prefix) bool {
	a = a.Unmap()
	for _, n := range nets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

func peerAddr(r *http.Request) (netip.Addr, string) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, perr := netip.ParseAddr(host)
	if perr != nil {
		return netip.Addr{}, host
	}
	return a.Unmap(), a.Unmap().String()
}

// resolveClientIP returns the address rate limits should key on. X-Forwarded-For is honoured ONLY when the
// direct peer is one of the configured trusted proxies, and then the list is read from the right: the first
// address that is not itself a trusted proxy is the client. Anything the client wrote further left cannot
// be used to dodge a limit, and a request that did not come through a trusted proxy is keyed on its peer.
func resolveClientIP(r *http.Request, trusted []netip.Prefix) string {
	peer, text := peerAddr(r)
	if !peer.IsValid() || len(trusted) == 0 || !inAny(peer, trusted) {
		return text
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return text // malformed header: do not guess, key on the proxy
		}
		a = a.Unmap()
		if !inAny(a, trusted) {
			return a.String()
		}
	}
	return text
}

// ClientIPs computes the client address once per request and stores it for ClientIP. trusted may be empty,
// in which case the TCP peer is always used.
func ClientIPs(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := resolveClientIP(r, trusted)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip)))
		})
	}
}

// ClientIP is the address to key rate limits on. Without the ClientIPs middleware (tests) it is the TCP peer.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok && ip != "" {
		return ip
	}
	_, text := peerAddr(r)
	return text
}
