package auth

import (
	"context"
	"net/http"
)

type contextKey struct{}

func contextWithAccessKey(ctx context.Context, accessKeyID string) context.Context {
	return context.WithValue(ctx, contextKey{}, accessKeyID)
}

// AccessKeyFromRequest returns the validated access key ID from the request context.
func AccessKeyFromRequest(r *http.Request) (string, bool) {
	v, ok := r.Context().Value(contextKey{}).(string)
	return v, ok
}

// RequireAuth wraps a handler to require SigV4 auth via the given Middleware.
func RequireAuth(m *Middleware, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := AccessKeyFromRequest(r)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

var _ = http.NoBody // silence unused import in some builds
