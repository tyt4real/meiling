package s3

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/auth"
	"meiling/internal/db"
	"meiling/internal/metrics"
	"meiling/internal/store"
)

// S3Handler routes bucket and object operations (path-style and virtual-host-style).
func S3Handler(pool *pgxpool.Pool, st store.ObjectStore, storageRoot string, ipfsAdd IPFSAddFunc, reg *metrics.Registry) http.Handler {
	bAPI := &BucketAPI{Pool: pool, Reg: reg}
	oAPI := &ObjectAPI{Pool: pool, Store: st, Reg: reg, StorageRoot: storageRoot, IPFSAdd: ipfsAdd}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucketName, key, err := splitBucketKey(r)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "InvalidURI", "Could not parse the request URI.", "")
			return
		}

		accessKey, ok := auth.AccessKeyFromRequest(r)
		if !ok {
			errorResponse(w, http.StatusUnauthorized, "AccessDenied", "Access denied.", "")
			return
		}
		keyObj, err := db.GetAccessKey(r.Context(), pool, accessKey)
		if err != nil {
			errorResponse(w, http.StatusUnauthorized, "AccessDenied", "Access denied.", "")
			return
		}
		accountID := keyObj.AccountID

		// Get account info for permission checks.
		_, accountRole, ok := auth.AccountFromRequest(r)
		isAdmin := ok && accountRole == db.RoleAdmin

		switch r.Method {
		case http.MethodGet:
			if bucketName == "" && key == "" {
				bAPI.ListBuckets(w, r, accountID, isAdmin)
				return
			}
			if key == "" {
				q := r.URL.Query()
				if q.Get("location") != "" {
					if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
						errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
						return
					}
					bAPI.GetBucketLocation(w, r, bucketName)
					return
				}
				if _, ok := q["uploads"]; ok {
					if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
						errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
						return
					}
					oAPI.ListMultipartUploadsByName(w, r, accountID, bucketName)
					return
				}
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				oAPI.ListObjectsV2ByName(w, r, accountID, bucketName)
				return
			}
			q := r.URL.Query()
			if q.Get("uploadId") != "" {
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				oAPI.ListPartsByName(w, r, accountID, bucketName, key)
				return
			}
			if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
				errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
				return
			}
			oAPI.GetObjectByName(w, r, accountID, bucketName, key)
		case http.MethodPut:
			if key == "" {
				bAPI.CreateBucket(w, r, accountID, bucketName)
				return
			}
			q := r.URL.Query()
			if q.Get("copy") != "" {
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				oAPI.CopyObjectByName(w, r, accountID, bucketName, key)
				return
			}
			if q.Get("uploadId") != "" && q.Get("partNumber") != "" {
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				oAPI.UploadPartByName(w, r, accountID, bucketName, key)
				return
			}
			if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
				errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
				return
			}
			oAPI.PutObjectByName(w, r, accountID, bucketName, key)
		case http.MethodPost:
			if key == "" {
				errorResponse(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed against this resource.", "")
				return
			}
			q := r.URL.Query()
			if _, ok := q["uploads"]; ok {
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				oAPI.CreateMultipartUploadByName(w, r, accountID, bucketName, key)
				return
			}
			if q.Get("uploadId") != "" {
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				oAPI.CompleteMultipartUploadByName(w, r, accountID, bucketName, key)
				return
			}
			if q.Get("delete") != "" {
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				oAPI.DeleteObjectsByName(w, r, accountID, bucketName)
				return
			}
			errorResponse(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed against this resource.", "")
		case http.MethodDelete:
			if key == "" {
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				bAPI.DeleteBucket(w, r, bucketName)
				return
			}
			if r.URL.Query().Get("uploadId") != "" {
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				oAPI.AbortMultipartUploadByName(w, r, accountID, bucketName, key)
				return
			}
			if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
				errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
				return
			}
			oAPI.DeleteObjectByName(w, r, accountID, bucketName, key)
		case http.MethodHead:
			if key == "" {
				if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
					errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
					return
				}
				bAPI.HeadBucket(w, r, bucketName)
				return
			}
			if err := checkBucketAccess(r.Context(), pool, accountID, isAdmin, bucketName); err != nil {
				errorResponse(w, http.StatusForbidden, "AccessDenied", err.Error(), bucketName)
				return
			}
			oAPI.HeadObjectByName(w, r, accountID, bucketName, key)
		default:
			errorResponse(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed against this resource.", "")
		}
	})
}

// checkBucketAccess verifies that the account has access to the bucket.
// Admins can access any bucket; users can only access their own buckets.
func checkBucketAccess(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, isAdmin bool, bucketName string) error {
	if isAdmin {
		return nil
	}
	b, err := db.GetBucketByName(ctx, pool, bucketName)
	if err != nil {
		return err
	}
	if b.AccountID != accountID {
		return fmt.Errorf("access denied to bucket %s", bucketName)
	}
	return nil
}

func splitBucketKey(r *http.Request) (bucket, key string, err error) {
	host := strings.TrimSuffix(strings.SplitN(r.Host, ":", 2)[0], ".")
	path := strings.TrimPrefix(r.URL.Path, "/")

	if host != "" && host != "localhost" && !strings.HasPrefix(host, "localhost:") {
		if path == "" {
			return host, "", nil
		}
		return host, path, nil
	}

	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 1 {
		return parts[0], "", nil
	}
	return parts[0], parts[1], nil
}
