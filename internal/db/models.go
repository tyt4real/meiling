package db

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("not found")

type Object struct {
	ID           uuid.UUID
	BucketID     uuid.UUID
	Key          string
	DataPath     string
	Size         int64
	ContentType  sql.NullString
	ETag         string
	CID          sql.NullString
	IPFSStatus   string
	UserMetadata sql.NullString
	CreatedAt    time.Time
}

type MultipartUpload struct {
	ID           uuid.UUID
	UploadID     string
	BucketID     uuid.UUID
	Key          string
	ContentType  sql.NullString
	UserMetadata sql.NullString
	CreatedAt    time.Time
}

type MultipartPart struct {
	UploadID   string
	PartNumber int
	Size       int64
	ETag       string
}
