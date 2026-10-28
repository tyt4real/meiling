// Package store provides object storage backends for the S3 API.
//
// ObjectStore decouples the S3 handlers from any particular storage backend.
// It distinguishes metadata operations (Head, Exists) from content streaming
// (Open). ObjectReader is built on Go's standard io.Reader, io.ReaderAt,
// io.Seeker and io.Closer interfaces so that range reads can be served
// efficiently without buffering the whole object in memory.
package store

import (
	"context"
	"io"
	"time"
)

// ObjectInfo describes an object's metadata. Fields that are not relevant to
// a particular backend are left at their zero value.
type ObjectInfo struct {
	Key           string
	Size          int64
	ContentType   string
	ETag          string
	CID           string
	StorageBackend string // "fs" or "ipfs"
	LastModified  time.Time
}

// ObjectReader allows sequential and random-access reads of an object.
type ObjectReader interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}

// ObjectStore is the abstraction that S3 object handlers depend on.
type ObjectStore interface {
	// Put stores an object, streaming from src without requiring the whole
	// body to be buffered in memory. contentType is the MIME type supplied by
	// the client. It returns the object's metadata.
	Put(ctx context.Context, bucket, key string, src io.Reader, contentType string) (ObjectInfo, error)
	// Head returns metadata for the object. ErrNotFound-style errors are
	// returned by backends when the object does not exist.
	Head(ctx context.Context, bucket, key string) (ObjectInfo, error)
	// Open returns a reader positioned at the start of the object. The
	// returned reader supports ReaderAt and Seeker so callers can perform
	// range reads.
	Open(ctx context.Context, bucket, key string) (ObjectReader, error)
	// Delete removes the object and its content.
	Delete(ctx context.Context, bucket, key string) error
	// Exists reports whether the object exists.
	Exists(ctx context.Context, bucket, key string) (bool, error)
}
