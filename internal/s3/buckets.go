package s3

import (
	"encoding/xml"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/db"
	"meiling/internal/metrics"
	"meiling/internal/store"
	"meiling/internal/validation"
)

// BucketAPI handles bucket-level S3 operations.
type BucketAPI struct {
	Pool  *pgxpool.Pool
	Reg   *metrics.Registry
	Store *store.FS
}

func (a *BucketAPI) ListBuckets(w http.ResponseWriter, r *http.Request, accountID uuid.UUID) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	buckets, err := db.ListBuckets(r.Context(), a.Pool, accountID)
	if err != nil {
		http.Error(w, "failed to list buckets", http.StatusInternalServerError)
		return
	}
	owner := &Owner{ID: accountID.String(), DisplayName: accountID.String()}
	var bk []Bucket
	for _, b := range buckets {
		bk = append(bk, Bucket{Name: b.Name, CreationDate: isoTime(b.CreatedAt)})
	}
	a.Reg.BucketsTotal.Set(int64(len(bk)))
	xmlResponse(w, http.StatusOK, ListAllMyBucketsResult{
		Owner:   owner,
		Buckets: bk,
	})
}

func (a *BucketAPI) CreateBucket(w http.ResponseWriter, r *http.Request, accountID uuid.UUID, name string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	if err := validation.ValidateBucketName(name); err != nil {
		errorResponse(w, http.StatusBadRequest, "InvalidBucketName", err.Error(), name)
		return
	}
	_, err := db.CreateBucket(r.Context(), a.Pool, accountID, name)
	if err != nil {
		errorResponse(w, http.StatusConflict, "BucketAlreadyExists", "The requested bucket name is not available.", name)
		return
	}
	w.Header().Set("Location", "/"+name)
	w.WriteHeader(http.StatusOK)
	xmlResponse(w, http.StatusOK, CreateBucketOutput{})
}

func (a *BucketAPI) HeadBucket(w http.ResponseWriter, r *http.Request, name string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	_, err := db.GetBucketByName(r.Context(), a.Pool, name)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", name)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *BucketAPI) DeleteBucket(w http.ResponseWriter, r *http.Request, name string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	if err := db.DeleteBucket(r.Context(), a.Pool, name); err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", name)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *BucketAPI) GetBucketLocation(w http.ResponseWriter, r *http.Request, name string) {
	start := time.Now()
	defer func() { a.Reg.HTTPRequestDuration.Observe(time.Since(start).Seconds()) }()
	a.Reg.HTTPRequestsTotal.Inc(1)

	_, err := db.GetBucketByName(r.Context(), a.Pool, name)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", name)
		return
	}
	xmlResponse(w, http.StatusOK, struct {
		XMLName            xml.Name `xml:"https://s3.amazonaws.com/doc/2006-03-01/ GetBucketLocationOutput"`
		LocationConstraint string   `xml:"LocationConstraint,omitempty"`
	}{LocationConstraint: "auto"})
}
