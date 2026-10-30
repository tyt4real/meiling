package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"
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

// AccountInfo holds account information from the access key.
type AccountInfo struct {
	AccountID uuid.UUID
	Role      string
}

// contextWithAccount stores the account info in the context.
func contextWithAccount(ctx context.Context, accountID uuid.UUID, role string) context.Context {
	ctx = context.WithValue(ctx, accountIDKey{}, accountID)
	return context.WithValue(ctx, accountRoleKey{}, role)
}

// AccountFromRequest returns the account info from the request context.
func AccountFromRequest(r *http.Request) (uuid.UUID, string, bool) {
	aid, ok := r.Context().Value(accountIDKey{}).(uuid.UUID)
	if !ok {
		return uuid.Nil, "", false
	}
	role, _ := r.Context().Value(accountRoleKey{}).(string)
	return aid, role, true
}

type accountIDKey struct{}
type accountRoleKey struct{}

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
