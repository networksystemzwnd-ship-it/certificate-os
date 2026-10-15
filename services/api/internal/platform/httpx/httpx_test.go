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
