package s3

import (
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

// ObjectAPI handles object-level S3 operations. It depends on the abstract
// ObjectStore so it can serve objects from the filesystem or from IPFS.
type ObjectAPI struct {
	Pool *pgxpool.Pool
	Store store.ObjectStore
	Reg  *metrics.Registry
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

// PutObject streams the request body into the store without buffering the
// entire object in memory. The store computes the size and ETag as the bytes
// flow through.
func (a *ObjectAPI) PutObject(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID, bucketName, key string, accountID uuid.UUID) {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Replace any existing object with the same key.
	old, serr := db.GetObjectByKey(r.Context(), a.Pool, bucketID, key)
	if serr == nil {
		_ = a.Store.Delete(r.Context(), bucketName, old.Key)
		_ = db.DeleteObject(r.Context(), a.Pool, bucketID, key)
	}

	info, err := a.Store.Put(r.Context(), bucketName, key, r.Body, contentType)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not store object.", key)
		return
	}

	dataPath := key
	if info.StorageBackend == "ipfs" && info.CID != "" {
		dataPath = info.CID
	}
	if _, err := db.CreateObject(r.Context(), a.Pool, bucketID, key, dataPath, info.Size, contentType, info.ETag); err != nil {
		errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not record object.", key)
		return
	}
	a.Reg.ObjectsTotal.Inc(1)
	w.Header().Set("ETag", "\""+info.ETag+"\"")
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

// GetObject returns the object, honouring HTTP byte ranges when a Range
// header is present. A satisfiable range yields 206 Partial Content with
// Content-Range and Content-Length; an unsatisfiable range yields 416 with
// Content-Range: bytes */<size>. Without a Range header, 200 OK is returned
// and the object is streamed in full.
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

	size := obj.Size
	etag := "\"" + obj.ETag + "\""

	w.Header().Set("ETag", etag)
	w.Header().Set("Accept-Ranges", "bytes")
	if obj.ContentType.Valid {
		w.Header().Set("Content-Type", obj.ContentType.String)
	}
	w.Header().Set("Last-Modified", isoTime(obj.CreatedAt))

	// If-Range: only honour the range if the validator matches; otherwise
	// return the full object as if no Range was given.
	hasRange := false
	var start, end int64
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		honorRange := true
		if ifRange := r.Header.Get("If-Range"); ifRange != "" && ifRange != etag {
			honorRange = false
		}
		if honorRange {
			parsed, err := parseRange(rangeHeader, size)
			switch err {
			case nil:
				hasRange = true
				start, end = parsed.start, parsed.end
			case errUnsatisfiable:
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			// malformed range: fall back to 200 as if no Range was given
		}
	}

	if !hasRange {
		rc, err := a.Store.Open(r.Context(), bucketName, key)
		if err != nil {
			errorResponse(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", key)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
		return
	}

	// Open the reader before committing headers so a backend failure can still
	// be reported with an appropriate status code.
	rc, err := a.Store.Open(r.Context(), bucketName, key)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", key)
		return
	}
	defer rc.Close()

	contentLength := end - start + 1
	w.Header().Set("Content-Length", fmt.Sprintf("%d", contentLength))
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	w.WriteHeader(http.StatusPartialContent)

	if start > 0 {
		if _, err := rc.Seek(start, io.SeekStart); err != nil {
			return
		}
	}
	_, _ = io.CopyN(w, rc, contentLength)
}

// parseRange parses a single "bytes=" range spec against the object size.
// It returns the inclusive [start, end] interval and nil when the range is
// satisfiable, errUnsatisfiable when the spec is valid but cannot be
// satisfied, or errMalformedRange for an invalid spec (caller falls back to 200).
func parseRange(spec string, size int64) (rangeResult, error) {
	spec = strings.TrimSpace(spec)
	if !strings.HasPrefix(spec, "bytes=") {
		return rangeResult{}, errMalformedRange
	}
	spec = strings.TrimPrefix(spec, "bytes=")
	if strings.Contains(spec, ",") {
		return rangeResult{}, errMalformedRange
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return rangeResult{}, errMalformedRange
	}

	// Suffix range: bytes=-N
	if parts[0] == "" {
		suffix, err := parseInt64(parts[1])
		if err != nil || suffix < 0 {
			return rangeResult{}, errMalformedRange
		}
		if size == 0 {
			return rangeResult{}, errUnsatisfiable
		}
		if suffix >= size {
			return rangeResult{start: 0, end: size - 1}, nil
		}
		return rangeResult{start: size - suffix, end: size - 1}, nil
	}

	start, err := parseInt64(parts[0])
	if err != nil || start < 0 {
		return rangeResult{}, errMalformedRange
	}
	if start >= size {
		return rangeResult{}, errUnsatisfiable
	}

	if parts[1] == "" {
		// bytes=start-
		return rangeResult{start: start, end: size - 1}, nil
	}

	end, err := parseInt64(parts[1])
	if err != nil || end < 0 {
		return rangeResult{}, errMalformedRange
	}
	if end > size-1 {
		end = size - 1
	}
	if start > end {
		return rangeResult{}, errUnsatisfiable
	}
	return rangeResult{start: start, end: end}, nil
}

var (
	errMalformedRange = fmt.Errorf("malformed range")
	errUnsatisfiable  = fmt.Errorf("unsatisfiable range")
)

type rangeResult struct {
	start, end int64
}

// parseInt64 parses a non-negative integer, rejecting values that overflow
// int64.
func parseInt64(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	var n int64
	for i, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit")
		}
		n = n*10 + int64(c-'0')
		if i > 0 && n < 0 {
			return 0, fmt.Errorf("overflow")
		}
	}
	return n, nil
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
	w.Header().Set("Accept-Ranges", "bytes")
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
	_ = a.Store.Delete(r.Context(), bucketName, key)
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
