package ipfs

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/db"
	"meiling/internal/metrics"
)

// IPFSHandler routes the IPFS pinning service API.
func IPFSHandler(pool *pgxpool.Pool, dist *Distributor, reg *metrics.Registry) http.Handler {
	api := &API{Pool: pool, Distributor: dist, Reg: reg}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucketID, err := authenticateBearer(r, pool)
		if err != nil {
			writeFailure(w, http.StatusUnauthorized, "ERR_INVALID_TOKEN", "api key does not exist")
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/ipfs")
		path = strings.TrimSuffix(path, "/")

		switch {
		case path == "/pins" || path == "":
			switch r.Method {
			case http.MethodGet:
				api.ListPins(w, r, bucketID)
			case http.MethodPost:
				api.AddPin(w, r, bucketID)
			default:
				writeFailure(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			}
			return
		}

		parts := strings.SplitN(path, "/", 3)
		if len(parts) == 3 && parts[1] == "pins" {
			requestID := parts[2]
			switch r.Method {
			case http.MethodGet:
				api.GetPin(w, r, bucketID, requestID)
			case http.MethodPost:
				api.ReplacePin(w, r, bucketID, requestID)
			case http.MethodDelete:
				api.DeletePin(w, r, bucketID, requestID)
			default:
				writeFailure(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			}
			return
		}

		writeFailure(w, http.StatusNotFound, "NOT_FOUND", "The specified resource was not found")
	})
}

func authenticateBearer(r *http.Request, pool *pgxpool.Pool) (uuid.UUID, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return uuid.Nil, errMissingToken
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	key, err := db.GetAccessKey(r.Context(), pool, token)
	if err != nil {
		return uuid.Nil, err
	}
	if key.BucketID == nil {
		return uuid.Nil, errNoBucket
	}
	return *key.BucketID, nil
}

var errMissingToken = &authErr{msg: "missing token"}
var errNoBucket = &authErr{msg: "bucket not bound to token"}

type authErr struct{ msg string }

func (e *authErr) Error() string { return e.msg }
