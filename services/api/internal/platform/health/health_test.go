package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func serve(p Pinger, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	Register(mux, p)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	return rr
}

func TestLivenessIgnoresDependencies(t *testing.T) {
	if rr := serve(fakePinger{errors.New("down")}, "/healthz"); rr.Code != 200 {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestReadyOK(t *testing.T) {
	if rr := serve(fakePinger{}, "/readyz"); rr.Code != 200 {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestReadyUnavailableDoesNotLeakError(t *testing.T) {
	rr := serve(fakePinger{errors.New("password authentication failed for user certos")}, "/readyz")
	if rr.Code != 503 || strings.Contains(rr.Body.String(), "password") {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}
