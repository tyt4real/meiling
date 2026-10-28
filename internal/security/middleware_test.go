package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	m := NewMiddleware(handler, 100, 100, []string{"*"}, 1024, false)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("missing DENY")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("missing CSP")
	}
}

func TestRateLimiting(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// 1 token per second, burst 1
	m := NewMiddleware(handler, 1, 1, []string{"*"}, 1024, false)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req) // consume token
	if rec.Code != http.StatusOK {
		t.Errorf("first request should be OK, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, req) // should be rate limited
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("second request should be rate limited, got %d", rec.Code)
	}
}

func TestCORS(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	m := NewMiddleware(handler, 100, 100, []string{"https://example.com"}, 1024, false)
	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Errorf("expected example.com origin, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}
