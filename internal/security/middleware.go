package security

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// tokenBucket is a simple stdlib token-bucket rate limiter.
type tokenBucket struct {
	mu        sync.Mutex
	tokens    float64
	rate      float64 // tokens per second
	burst     float64
	last      time.Time
}

func newTokenBucket(rps float64, burst int) *tokenBucket {
	return &tokenBucket{
		tokens: float64(burst),
		rate:   rps,
		burst:  float64(burst),
		last:   time.Now(),
	}
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Middleware wraps an http.Handler with security controls.
type Middleware struct {
	Next             http.Handler
	RateLimiter      *tokenBucket
	CORSAllowOrigins []string
	MaxBodyBytes     int64
	EnableHTTPS      bool
}

// NewMiddleware constructs a security middleware.
func NewMiddleware(next http.Handler, rps float64, burst int, corsOrigins []string, maxBodyBytes int64, enableHTTPS bool) *Middleware {
	return &Middleware{
		Next:             next,
		RateLimiter:      newTokenBucket(rps, burst),
		CORSAllowOrigins: corsOrigins,
		MaxBodyBytes:     maxBodyBytes,
		EnableHTTPS:      enableHTTPS,
	}
}

func (m *Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.EnableHTTPS && r.TLS == nil && !strings.HasPrefix(r.Host, "localhost") {
		http.Error(w, "HTTPS required", http.StatusForbidden)
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	if m.EnableHTTPS {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}

	if r.Method == http.MethodOptions {
		m.handleCORS(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	m.handleCORS(w, r)

	if !m.RateLimiter.allow() {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, m.MaxBodyBytes)

	m.Next.ServeHTTP(w, r)
}

func (m *Middleware) handleCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	allowed := "*"
	if len(m.CORSAllowOrigins) > 0 {
		allowed = ""
		for _, o := range m.CORSAllowOrigins {
			if o == "*" {
				allowed = "*"
				break
			}
			if o == origin {
				allowed = origin
				break
			}
		}
	}
	if allowed != "" {
		w.Header().Set("Access-Control-Allow-Origin", allowed)
	}
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Content-Length, X-Amz-Date, X-Amz-Content-Sha256, X-Amz-User-Agent")
	w.Header().Set("Access-Control-Max-Age", "86400")
}
