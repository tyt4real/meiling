package s3

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/db"
	"meiling/internal/metrics"
	"meiling/internal/store"
	"meiling/internal/validation"
)

// ObjectAPI handles object-level S3 operations.
type ObjectAPI struct {
	Pool  *pgxpool.Pool
	Store *store.FS
	Reg   *metrics.Registry
}

func bucketIDByName(pool *pgxpool.Pool, r *http.Request, name string) (uuid.UUID, error) {
	b, err := db.GetBucketByName(r.Context(), pool, name)
	if err != nil {
		return uuid.Nil, err
	}
	return b.ID, nil
}

func (a *ObjectAPI) PutObjectByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName, key string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	if err := validation.ValidateBucketName(bucketName); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidBucketName", err.Error(), bucketName)
		return
	}
	if err := validation.ValidateObjectKey(key); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidKey", err.Error(), key)
		return
	}

	bucketID, err := bucketIDByName(a.Pool, r, bucketName)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", bucketName)
		return
	}
	a.PutObject(w, r, bucketID, bucketName, key, accountID)
}

func (a *ObjectAPI) PutObject(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID, bucketName, key string, accountID uuid.UUID) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "Could not read request body.", key)
		return
	}

	old, serr := db.GetObjectByKey(r.Context(), a.Pool, bucketID, key)
	if serr == nil {
		_ = a.Store.Delete(bucketName, old.Key)
		_ = db.DeleteObject(r.Context(), a.Pool, bucketID, key)
	}
	etag := fmt.Sprintf("%x", sha256.Sum256(body))
	size := int64(len(body))
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if _, err := a.Store.Put(bucketName, key, strings.NewReader(string(body))); err != nil {
		errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not store object.", key)
		return
	}
	if _, err := db.CreateObject(r.Context(), a.Pool, bucketID, key, key, size, contentType, etag); err != nil {
		errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not record object.", key)
		return
	}
	a.Reg.ObjectsTotal.Inc(1)
	w.Header().Set("ETag", "\""+etag+"\"")
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

func (a *ObjectAPI) GetObjectByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName, key string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	if err := validation.ValidateBucketName(bucketName); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidBucketName", err.Error(), bucketName)
		return
	}
	if err := validation.ValidateObjectKey(key); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidKey", err.Error(), key)
		return
	}

	bucketID, err := bucketIDByName(a.Pool, r, bucketName)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", bucketName)
		return
	}
	a.GetObject(w, r, bucketID, bucketName, key)
}

func (a *ObjectAPI) GetObject(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID, bucketName, key string) {
	obj, err := db.GetObjectByKey(r.Context(), a.Pool, bucketID, key)
	if err != nil {
		if err == db.ErrNotFound {
			errorResponse(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", key)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	rc, size, err := a.Store.Get(bucketName, key)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", key)
		return
	}
	defer rc.Close()
	w.Header().Set("ETag", "\""+obj.ETag+"\"")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	if obj.ContentType.Valid {
		w.Header().Set("Content-Type", obj.ContentType.String)
	}
	w.Header().Set("Last-Modified", isoTime(obj.CreatedAt))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (a *ObjectAPI) HeadObjectByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName, key string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	if err := validation.ValidateBucketName(bucketName); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidBucketName", err.Error(), bucketName)
		return
	}
	if err := validation.ValidateObjectKey(key); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidKey", err.Error(), key)
		return
	}

	bucketID, err := bucketIDByName(a.Pool, r, bucketName)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", bucketName)
		return
	}
	a.HeadObject(w, r, bucketID, bucketName, key)
}

func (a *ObjectAPI) HeadObject(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID, bucketName, key string) {
	obj, err := db.GetObjectByKey(r.Context(), a.Pool, bucketID, key)
	if err != nil {
		if err == db.ErrNotFound {
			errorResponse(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", key)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("ETag", "\""+obj.ETag+"\"")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", obj.Size))
	if obj.ContentType.Valid {
		w.Header().Set("Content-Type", obj.ContentType.String)
	}
	w.Header().Set("Last-Modified", isoTime(obj.CreatedAt))
	w.WriteHeader(http.StatusOK)
}

func (a *ObjectAPI) DeleteObjectByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName, key string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	if err := validation.ValidateBucketName(bucketName); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidBucketName", err.Error(), bucketName)
		return
	}
	if err := validation.ValidateObjectKey(key); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidKey", err.Error(), key)
		return
	}

	bucketID, err := bucketIDByName(a.Pool, r, bucketName)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", bucketName)
		return
	}
	a.DeleteObject(w, r, bucketID, bucketName, key)
}

func (a *ObjectAPI) DeleteObject(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID, bucketName, key string) {
	_, err := db.GetObjectByKey(r.Context(), a.Pool, bucketID, key)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", key)
		return
	}
	_ = a.Store.Delete(bucketName, key)
	if err := db.DeleteObject(r.Context(), a.Pool, bucketID, key); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *ObjectAPI) ListObjectsV2ByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	prefix := r.URL.Query().Get("prefix")
	marker := r.URL.Query().Get("marker")
	startAfter := r.URL.Query().Get("start-after")
	limit := 1000
	if v := r.URL.Query().Get("max-keys"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if startAfter != "" {
		marker = startAfter
	}

	bucketID, err := bucketIDByName(a.Pool, r, bucketName)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", bucketName)
		return
	}
	objs, err := db.ListObjects(r.Context(), a.Pool, bucketID, prefix, marker, limit)
	if err != nil {
		http.Error(w, "failed to list objects", http.StatusInternalServerError)
		return
	}
	truncated := false
	var contents []Object
	for _, o := range objs {
		contents = append(contents, Object{
			Key:          o.Key,
			LastModified: isoTime(o.CreatedAt),
			ETag:         "\"" + o.ETag + "\"",
			Size:         o.Size,
			StorageClass: "STANDARD",
			Owner: &Owner{
				ID:          accountID.String(),
				DisplayName: accountID.String(),
			},
		})
	}
	if len(contents) == limit {
		truncated = true
	}
	xmlResponse(w, http.StatusOK, ListBucketResult{
		BucketName:  bucketName,
		Prefix:      prefix,
		Marker:      marker,
		MaxKeys:     limit,
		IsTruncated: truncated,
		Contents:    contents,
	})
}
