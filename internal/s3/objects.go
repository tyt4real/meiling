package s3

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/db"
	"meiling/internal/metrics"
	"meiling/internal/store"
	"meiling/internal/validation"
)

// IPFSAddFunc adds content to an IPFS node and returns its CID and size.
// It is nil when IPFS uploads are not configured, in which case PutObject
// stores locally only.
type IPFSAddFunc func(ctx context.Context, src io.Reader, name string) (cid string, size int64, err error)

// ObjectAPI handles object-level S3 operations. It depends on the abstract
// ObjectStore so it can serve objects from the filesystem or from IPFS.
type ObjectAPI struct {
	Pool      *pgxpool.Pool
	Store     store.ObjectStore
	Reg       *metrics.Registry
	StorageRoot string // root used for temp files and final object placement
	IPFSAdd   IPFSAddFunc // optional: add uploaded content to an IPFS node
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

// ingestObject stores an object: it streams src to a temp file on disk
// (computing an MD5 ETag as it goes), optionally adds the content to an IPFS
// node, then atomically renames the temp file into place and records the object
// in PostgreSQL. Any previously stored object under the same bucket/key is
// replaced.
//
// If an IPFS adder is configured and the add succeeds, the row is marked
// ipfs_status='pinned' with the CID. If the IPFS add fails, the local copy is
// kept, the row is marked ipfs_status='pending' (the reconciler retries later),
// and no CID is returned. This ensures a failed node never leaves a permanently
// visible object with missing content.
func (a *ObjectAPI) ingestObject(ctx context.Context, bucketID uuid.UUID, bucketName, key string, accountID uuid.UUID, contentType string, userMetadata map[string]string, etagOverride string, src io.Reader) (*store.ObjectInfo, error) {
	// Replace any existing object with the same key.
	old, serr := db.GetObjectByKey(ctx, a.Pool, bucketID, key)
	if serr == nil {
		_ = a.Store.Delete(ctx, bucketName, old.Key)
		_ = db.DeleteObject(ctx, a.Pool, bucketID, key)
	}

	// Stream the body to a temp file while computing the MD5 ETag. The whole
	// body is streamed to disk, never held in RAM.
	if err := os.MkdirAll(filepath.Join(a.StorageRoot, ".tmp"), 0750); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Join(a.StorageRoot, ".tmp"), "upload-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }

	h := md5.New()
	dst := io.MultiWriter(tmp, h)
	n, err := io.Copy(dst, src)
	_ = tmp.Close()
	if err != nil {
		cleanup()
		return nil, err
	}
	etag := etagOverride
	if etag == "" {
		etag = fmt.Sprintf("%x", h.Sum(nil))
	}

	finalPath := filepath.Join(a.StorageRoot, "buckets", bucketName, key)

	ipfsStatus := "none"
	var cid string
	var addErr error

	if a.IPFSAdd != nil {
		// Attempt to add the complete local file to IPFS.
		fh, err := os.Open(tmpPath)
		if err != nil {
			cleanup()
			return nil, err
		}
		cid, _, addErr = a.IPFSAdd(ctx, fh, key)
		fh.Close()
		if addErr != nil {
			// Node unavailable mid-upload: keep the local copy, mark pending,
			// the reconciler retries the add.
			ipfsStatus = "pending"
			if _, moveErr := moveFile(tmpPath, finalPath); moveErr != nil {
				cleanup()
				return nil, moveErr
			}
		} else {
			ipfsStatus = "pinned"
			if _, moveErr := moveFile(tmpPath, finalPath); moveErr != nil {
				cleanup()
				return nil, moveErr
			}
		}
	} else {
		if _, moveErr := moveFile(tmpPath, finalPath); moveErr != nil {
			cleanup()
			return nil, moveErr
		}
	}

	obj, err := db.CreateObject(ctx, a.Pool, bucketID, key, finalPath, n, contentType, etag, cid, ipfsStatus, userMetadata)
	if err != nil {
		_ = a.Store.Delete(ctx, bucketName, key)
		return nil, err
	}
	a.Reg.ObjectsTotal.Inc(1)

	return &store.ObjectInfo{
		Key:            obj.Key,
		Size:           obj.Size,
		ContentType:    contentType,
		ETag:           etag,
		CID:            cid,
		StorageBackend: "fs",
	}, nil
}

// PutObject ingests a single PUT object: it streams the request body through
// ingestObject and responds with the ETag, the CID in x-amz-meta-cid (when
// IPFS uploads succeeded), and any x-amz-meta-* user metadata headers.
func (a *ObjectAPI) PutObject(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID, bucketName, key string, accountID uuid.UUID) {
	ctx := r.Context()
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	userMetadata := collectUserMetadata(r.Header)

	info, err := a.ingestObject(ctx, bucketID, bucketName, key, accountID, contentType, userMetadata, "", r.Body)
	if err != nil {
		slog.Error("put: ingest failed", "bucket", bucketName, "key", key, "error", err)
		errorResponse(w, http.StatusInternalServerError, "InternalError", fmt.Sprintf("Could not store object: %v", err), key)
		return
	}

	w.Header().Set("ETag", "\""+info.ETag+"\"")
	if info.CID != "" {
		w.Header().Set("X-Amz-Meta-Cid", info.CID)
	}
	for name, val := range userMetadata {
		w.Header().Set("X-Amz-Meta-"+name, val)
	}
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

// collectUserMetadata extracts x-amz-meta-* headers into a map.
func collectUserMetadata(h http.Header) map[string]string {
	userMetadata := make(map[string]string)
	for k, vv := range h {
		if !strings.HasPrefix(k, "X-Amz-Meta-") {
			continue
		}
		if len(vv) == 0 {
			continue
		}
		name := strings.TrimPrefix(k, "X-Amz-Meta-")
		userMetadata[name] = vv[0]
	}
	return userMetadata
}

// moveFile atomically moves src to dst, creating parent directories as needed.
func moveFile(src, dst string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		slog.Error("put: mkdir failed", "dst", dst, "error", err)
		return false, err
	}
	if err := os.Rename(src, dst); err != nil {
		slog.Error("put: rename failed", "src", src, "dst", dst, "error", err)
		// Cross-device fallback: copy then remove.
		if err := copyFile(src, dst); err != nil {
			return false, err
		}
		_ = os.Remove(src)
	}
	return true, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func (a *ObjectAPI) partDir(uploadID string) string {
	return filepath.Join(a.StorageRoot, ".multipart", uploadID)
}

func (a *ObjectAPI) partPath(uploadID string, partNumber int) string {
	return filepath.Join(a.partDir(uploadID), fmt.Sprintf("%d", partNumber))
}

// CreateMultipartUploadByName initiates a multipart upload.
func (a *ObjectAPI) CreateMultipartUploadByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName, key string) {
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
	contentType := r.Header.Get("Content-Type")
	userMetadata := collectUserMetadata(r.Header)

	mu, err := db.CreateMultipartUpload(r.Context(), a.Pool, bucketID, key, contentType, userMetadata)
	if err != nil {
		slog.Error("create multipart upload failed", "bucket", bucketName, "key", key, "error", err)
		errorResponse(w, http.StatusInternalServerError, "InternalError", fmt.Sprintf("Could not initiate multipart upload: %v", err), key)
		return
	}
	xmlResponse(w, http.StatusOK, InitiateMultipartUploadResult{
		Bucket:   bucketName,
		Key:      key,
		UploadId: mu.UploadID,
	})
}

// UploadPartByName uploads a single part of a multipart upload.
func (a *ObjectAPI) UploadPartByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName, key string) {
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
	q := r.URL.Query()
	uploadID := q.Get("uploadId")
	partNumber, err := strconv.Atoi(q.Get("partNumber"))
	if err != nil || partNumber < 1 || partNumber > 10000 {
		errorResponse(w, http.StatusBadRequest, "InvalidArgument", "partNumber must be an integer between 1 and 10000.", key)
		return
	}

	mu, err := db.GetMultipartUpload(r.Context(), a.Pool, uploadID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchUpload", "The specified upload does not exist.", key)
		return
	}
	if mu.BucketID != bucketID || mu.Key != key {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "The upload does not belong to this bucket/key.", key)
		return
	}

	if err := os.MkdirAll(a.partDir(uploadID), 0750); err != nil {
		errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not prepare parts directory.", key)
		return
	}
	partPath := a.partPath(uploadID, partNumber)
	f, err := os.Create(partPath)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not create part file.", key)
		return
	}
	defer f.Close()
	h := md5.New()
	n, err := io.Copy(io.MultiWriter(f, h), r.Body)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "Could not read part body.", key)
		return
	}
	etag := fmt.Sprintf("%x", h.Sum(nil))
	if err := db.CreateMultipartPart(r.Context(), a.Pool, uploadID, partNumber, n, etag); err != nil {
		errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not record part.", key)
		return
	}
	w.Header().Set("ETag", "\""+etag+"\"")
	w.WriteHeader(http.StatusOK)
}

// CompleteMultipartUploadByName completes a multipart upload.
func (a *ObjectAPI) CompleteMultipartUploadByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName, key string) {
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
	q := r.URL.Query()
	uploadID := q.Get("uploadId")

	mu, err := db.GetMultipartUpload(r.Context(), a.Pool, uploadID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchUpload", "The specified upload does not exist.", key)
		return
	}
	if mu.BucketID != bucketID || mu.Key != key {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "The upload does not belong to this bucket/key.", key)
		return
	}

	var req CompleteMultipartUpload
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "Could not parse completion request.", key)
		return
	}
	if len(req.Parts) == 0 {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "At least one part is required.", key)
		return
	}

	parts, err := db.ListMultipartParts(r.Context(), a.Pool, uploadID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not list parts.", key)
		return
	}
	stored := make(map[int]db.MultipartPart)
	for _, p := range parts {
		stored[p.PartNumber] = p
	}

	// Validate contiguity and ETags.
	expectedETags := make([]string, 0, len(req.Parts))
	readers := make([]io.Reader, 0, len(req.Parts))
	prev := 0
	for _, p := range req.Parts {
		if p.PartNumber <= prev {
			errorResponse(w, http.StatusBadRequest, "InvalidPartOrder", "Parts must be listed in ascending order.", key)
			return
		}
		if p.PartNumber-prev != 1 {
			errorResponse(w, http.StatusBadRequest, "InvalidPartOrder", "Parts must be contiguous starting at 1.", key)
			return
		}
		prev = p.PartNumber
		sp, ok := stored[p.PartNumber]
		if !ok {
			errorResponse(w, http.StatusBadRequest, "InvalidPart", "One or more parts could not be found.", key)
			return
		}
		// Strip surrounding quotes from the supplied ETag for comparison.
		supplied := strings.Trim(p.ETag, "\"")
		if supplied != sp.ETag {
			errorResponse(w, http.StatusBadRequest, "InvalidPart", "One or more parts ETag did not match.", key)
			return
		}
		expectedETags = append(expectedETags, sp.ETag)
		partReader, err := os.Open(a.partPath(uploadID, p.PartNumber))
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not open part file.", key)
			return
		}
		readers = append(readers, partReader)
	}

	// Final ETag: MD5 of the concatenated part ETags suffixed with "-N".
	h := md5.New()
	for _, e := range expectedETags {
		io.WriteString(h, e)
	}
	finalEtag := fmt.Sprintf("%x-%d", h.Sum(nil), len(expectedETags))

	contentType := mu.ContentType.String
	if !mu.ContentType.Valid {
		contentType = "application/octet-stream"
	}
	userMetadata := make(map[string]string)
	if mu.UserMetadata.Valid {
		_ = json.Unmarshal([]byte(mu.UserMetadata.String), &userMetadata)
	}

	info, err := a.ingestObject(r.Context(), bucketID, bucketName, key, accountID, contentType, userMetadata, finalEtag, io.MultiReader(readers...))
	// Close all part readers.
	for _, rd := range readers {
		if c, ok := rd.(io.Closer); ok {
			_ = c.Close()
		}
	}
	if err != nil {
		slog.Error("complete multipart: ingest failed", "bucket", bucketName, "key", key, "error", err)
		errorResponse(w, http.StatusInternalServerError, "InternalError", fmt.Sprintf("Could not complete multipart upload: %v", err), key)
		return
	}

	// Remove the multipart upload record and staged parts.
	_ = db.DeleteMultipartUpload(r.Context(), a.Pool, uploadID)
	_ = os.RemoveAll(a.partDir(uploadID))

	w.Header().Set("ETag", "\""+info.ETag+"\"")
	if info.CID != "" {
		w.Header().Set("X-Amz-Meta-Cid", info.CID)
	}
	xmlResponse(w, http.StatusOK, CompleteMultipartUploadResult{
		Location: "/"+bucketName+"/"+key,
		Bucket:   bucketName,
		Key:      key,
		ETag:     info.ETag,
	})
}

// AbortMultipartUploadByName aborts a multipart upload and removes its parts.
func (a *ObjectAPI) AbortMultipartUploadByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName, key string) {
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
	q := r.URL.Query()
	uploadID := q.Get("uploadId")

	mu, err := db.GetMultipartUpload(r.Context(), a.Pool, uploadID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchUpload", "The specified upload does not exist.", key)
		return
	}
	if mu.BucketID != bucketID || mu.Key != key {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "The upload does not belong to this bucket/key.", key)
		return
	}

	_ = db.DeleteMultipartUpload(r.Context(), a.Pool, uploadID)
	_ = os.RemoveAll(a.partDir(uploadID))
	w.WriteHeader(http.StatusNoContent)
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

// CopyObjectByName copies an existing object by re-pointing at its data (CID
// for IPFS-backed objects, file path for filesystem-backed objects) without
// re-ingesting the content.
func (a *ObjectAPI) CopyObjectByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName, key string) {
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

	dstBucketID, err := bucketIDByName(a.Pool, r, bucketName)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", bucketName)
		return
	}

	copySource := r.Header.Get("X-Amz-Copy-Source")
	if copySource == "" {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "Missing x-amz-copy-source header.", key)
		return
	}
	srcBucket, srcKey, err := parseCopySource(copySource)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "Invalid x-amz-copy-source value.", key)
		return
	}

	srcBucketID, err := bucketIDByName(a.Pool, r, srcBucket)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified source bucket does not exist.", srcBucket)
		return
	}
	src, err := db.GetObjectByKey(r.Context(), a.Pool, srcBucketID, srcKey)
	if err != nil {
		if err == db.ErrNotFound {
			errorResponse(w, http.StatusNotFound, "NoSuchKey", "The specified source key does not exist.", srcKey)
		} else {
			errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not read source object.", key)
		}
		return
	}

	contentType := r.Header.Get("X-Amz-Content-Type")
	if contentType == "" && src.ContentType.Valid {
		contentType = src.ContentType.String
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	userMetadata := make(map[string]string)
	if src.UserMetadata.Valid {
		_ = json.Unmarshal([]byte(src.UserMetadata.String), &userMetadata)
	}

	if _, err := db.CreateObject(r.Context(), a.Pool, dstBucketID, key, src.DataPath, src.Size, contentType, src.ETag, src.CID.String, src.IPFSStatus, userMetadata); err != nil {
		slog.Error("copy: record object failed", "bucket", bucketName, "key", key, "error", err)
		errorResponse(w, http.StatusInternalServerError, "InternalError", "Could not record object.", key)
		return
	}
	a.Reg.ObjectsTotal.Inc(1)

	w.Header().Set("ETag", "\""+src.ETag+"\"")
	xmlResponse(w, http.StatusOK, CopyObjectResult{
		LastModified: isoTime(src.CreatedAt),
		ETag:         src.ETag,
	})
}

// DeleteObjectsByName deletes multiple objects in a single request.
func (a *ObjectAPI) DeleteObjectsByName(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, bucketName string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	if err := validation.ValidateBucketName(bucketName); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidBucketName", err.Error(), bucketName)
		return
	}
	bucketID, err := bucketIDByName(a.Pool, r, bucketName)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", bucketName)
		return
	}

	var req DeleteObjectsRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidRequest", "Could not parse delete request.", bucketName)
		return
	}

	var deleted []DeletedObject
	for _, obj := range req.Objects {
		if err := validation.ValidateObjectKey(obj.Key); err != nil {
			continue
		}
		o, err := db.GetObjectByKey(r.Context(), a.Pool, bucketID, obj.Key)
		if err != nil {
			continue
		}
		_ = a.Store.Delete(r.Context(), bucketName, o.Key)
		_ = db.DeleteObject(r.Context(), a.Pool, bucketID, obj.Key)
		deleted = append(deleted, DeletedObject{Key: obj.Key})
	}

	xmlResponse(w, http.StatusOK, DeleteResult{Deleted: deleted})
}

// parseCopySource parses an x-amz-copy-source value of the form /bucket/key
// (URL-encoded) into its bucket and key components.
func parseCopySource(s string) (string, string, error) {
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return "", "", fmt.Errorf("empty copy source")
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("copy source must be /bucket/key")
	}
	srcBucket, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", err
	}
	srcKey, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", err
	}
	return srcBucket, srcKey, nil
}
