package httpx

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	h := Chain(quiet, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestID(r.Context()) == "" {
			t.Error("missing request id in context")
		}
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Header().Get(RequestIDHeader) == "" {
		t.Fatal("request id not echoed")
	}
}

func TestMalformedInboundRequestIDReplaced(t *testing.T) {
	h := Chain(quiet, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(RequestIDHeader, "bad id\nwith newline")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get(RequestIDHeader); strings.ContainsAny(got, " \n") || got == "" {
		t.Fatalf("unsafe id passed through: %q", got)
	}
}

func TestPanicBecomesNormalized500(t *testing.T) {
	h := Chain(quiet, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("secret internals") }))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 500 {
		t.Fatalf("status %d", rr.Code)
	}
	var env struct{ Error ErrorBody }
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != CodeInternal || strings.Contains(rr.Body.String(), "secret internals") {
		t.Fatalf("bad body: %s", rr.Body.String())
	}
	if env.Error.RequestID == "" {
		t.Fatal("error envelope missing request_id")
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := Chain(quiet, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
}

func TestCSRFRequiresHeaderAndOrigin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := Chain(quiet, CSRF("http://web", ok))
	cases := []struct {
		name, method, header, origin string
		want                         int
	}{
		{"get needs nothing", "GET", "", "", 204},
		{"post without header", "POST", "", "", 403},
		{"post with header", "POST", "1", "", 204},
		{"post with header and good origin", "POST", "1", "http://web", 204},
		{"post with header, evil origin", "POST", "1", "http://evil", 403},
		{"delete without header", "DELETE", "", "", 403},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/", nil)
		if c.header != "" {
			req.Header.Set(CSRFHeader, c.header)
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != c.want {
			t.Errorf("%s: got %d want %d", c.name, rr.Code, c.want)
		}
	}
}

func TestCORSOnlyAllowedOrigin(t *testing.T) {
	h := CORS("http://web", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "http://web")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "http://web" || rr.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("allowed origin not granted")
	}

	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "http://evil")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("foreign origin must get no CORS headers")
	}

	req = httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://web")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 || !strings.Contains(rr.Header().Get("Access-Control-Allow-Headers"), CSRFHeader) ||
		!strings.Contains(rr.Header().Get("Access-Control-Allow-Headers"), "X-Institute-ID") {
		t.Fatalf("preflight: %d %v", rr.Code, rr.Header())
	}
}

func TestDecodeJSONStrict(t *testing.T) {
	type body struct{ Email string }
	try := func(ct, payload string) error {
		req := httptest.NewRequest("POST", "/", strings.NewReader(payload))
		req.Header.Set("Content-Type", ct)
		var b body
		return DecodeJSON(httptest.NewRecorder(), req, &b)
	}
	if err := try("application/json", `{"email":"a"}`); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][2]string{
		"unknown field": {"application/json", `{"email":"a","role":"admin"}`},
		"trailing data": {"application/json", `{"email":"a"}{"x":1}`},
		"wrong type":    {"text/plain", `{"email":"a"}`},
		"malformed":     {"application/json", `{`},
		"oversized":     {"application/json", `{"email":"` + strings.Repeat("a", 70<<10) + `"}`},
	} {
		if try(c[0], c[1]) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
