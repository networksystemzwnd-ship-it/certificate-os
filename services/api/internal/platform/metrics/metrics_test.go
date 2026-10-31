package metrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/platform/metrics"
)

func TestRenderCountersHistogramsAndGauges(t *testing.T) {
	r := metrics.New()
	r.Describe("cos_x_total", "Things.")
	r.Inc("cos_x_total", "code", "a")
	r.Inc("cos_x_total", "code", "a")
	r.Add("cos_x_total", 3, "code", "b\"q\nz")
	r.Observe("cos_d_seconds", 0.02, "method", "GET")
	r.Observe("cos_d_seconds", 20, "method", "GET")
	r.GaugeFunc("cos_depth", "Depth.", func(context.Context) []metrics.Sample {
		return []metrics.Sample{{Labels: map[string]string{"state": "overdue"}, Value: 2}, {Labels: map[string]string{"state": "active"}, Value: 5}}
	})
	out := r.Render(context.Background())
	for _, want := range []string{
		"# HELP cos_x_total Things.", "# TYPE cos_x_total counter", `cos_x_total{code="a"} 2`, `cos_x_total{code="b\"q\nz"} 3`,
		"# TYPE cos_d_seconds histogram", `cos_d_seconds_bucket{method="GET",le="0.01"} 0`, `cos_d_seconds_bucket{method="GET",le="0.05"} 1`,
		`cos_d_seconds_bucket{method="GET",le="+Inf"} 2`, `cos_d_seconds_count{method="GET"} 2`,
		"# TYPE cos_depth gauge", `cos_depth{state="active"} 5`, `cos_depth{state="overdue"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "\n\n") > 0 {
		t.Error("blank line in exposition")
	}
}

func TestNilRegistryIsSafe(t *testing.T) {
	var r *metrics.Registry
	r.Inc("a")
	r.Add("a", 2)
	r.Observe("a", 1)
	r.Describe("a", "b")
	r.GaugeFunc("a", "b", nil)
	if r.Render(context.Background()) != "" {
		t.Fatal("nil render")
	}
	h := r.HTTP(http.NotFoundHandler())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 404 {
		t.Fatal(rr.Code)
	}
}

func TestScrapeEndpointNeedsTheTokenAndIsOffByDefault(t *testing.T) {
	r := metrics.New()
	r.Inc("cos_x_total")
	get := func(h http.Handler, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/metrics", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	if rr := get(r.Handler(""), "Bearer anything"); rr.Code != 404 {
		t.Errorf("disabled: %d", rr.Code)
	}
	h := r.Handler("s3cret-token")
	for _, a := range []string{"", "Bearer wrong", "s3cret-token", "Basic s3cret-token", "Bearer s3cret-token2", "Bearer "} {
		if rr := get(h, a); rr.Code != 401 || strings.Contains(rr.Body.String(), "cos_x_total") {
			t.Errorf("auth %q: %d", a, rr.Code)
		}
	}
	rr := get(h, "Bearer s3cret-token")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "cos_x_total 1") || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %q", rr.Code, rr.Body.String())
	}
}

func TestHTTPMiddlewareUsesNoPathOrIdentity(t *testing.T) {
	r := metrics.New()
	h := r.HTTP(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.URL.Path, "missing") {
			http.NotFound(w, req)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	for _, p := range []string{"/api/v1/students/123-secret-id", "/missing/asha@example.com"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", p, nil))
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("BREW", "/", nil))
	out := r.Render(context.Background())
	for _, want := range []string{`cos_http_requests_total{class="2xx",method="POST"} 1`, `cos_http_requests_total{class="4xx",method="POST"} 1`, `method="OTHER"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, leak := range []string{"secret", "asha", "students", "missing"} {
		if strings.Contains(out, leak) {
			t.Errorf("metrics leak %q", leak)
		}
	}
}
