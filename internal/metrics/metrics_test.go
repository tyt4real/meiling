package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCounterInc(t *testing.T) {
	c := &Counter{}
	c.Inc(5)
	if c.Value() != 5 {
		t.Errorf("expected 5, got %d", c.Value())
	}
}

func TestGauge(t *testing.T) {
	g := &Gauge{}
	g.Inc()
	if g.Value() != 1 {
		t.Errorf("expected 1, got %d", g.Value())
	}
	g.Dec()
	if g.Value() != 0 {
		t.Errorf("expected 0, got %d", g.Value())
	}
}

func TestHistogramObserve(t *testing.T) {
	h := NewHistogram([]float64{0.1, 1.0})
	h.Observe(0.5)
	h.Observe(2.0)
	if h.count.Load() != 2 {
		t.Errorf("expected count 2, got %d", h.count.Load())
	}
}

func TestRegistryHandler(t *testing.T) {
	reg := NewRegistry()
	reg.HTTPRequestsTotal.Inc(3)
	handler := reg.Handler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "meiling_http_requests_total") {
		t.Error("missing http_requests_total metric")
	}
	if !strings.Contains(body, "meiling_http_request_duration_seconds") {
		t.Error("missing duration histogram")
	}
	if !strings.Contains(body, "# TYPE meiling_http_requests_total counter") {
		t.Error("missing type line")
	}
}
