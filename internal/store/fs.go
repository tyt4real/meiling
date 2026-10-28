package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// FS stores objects on the local filesystem.
type FS struct {
	Root string
}

func (f *FS) path(bucket, key string) string {
	return filepath.Join(f.Root, "buckets", bucket, key)
}

// Put stores an object by streaming src to disk. It computes the SHA-256 ETag
// as the bytes flow through, so the whole body is never held in memory.
func (f *FS) Put(ctx context.Context, bucket, key string, src io.Reader, contentType string) (ObjectInfo, error) {
	p := f.path(bucket, key)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return ObjectInfo{}, err
	}
	out, err := os.Create(p)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer out.Close()

	h := sha256.New()
	writer := io.MultiWriter(out, h)
	n, err := io.Copy(writer, src)
	if err != nil {
		os.Remove(p)
		return ObjectInfo{}, err
	}
	etag := fmt.Sprintf("%x", h.Sum(nil))
	return ObjectInfo{
		Key:            key,
		Size:           n,
		ContentType:    contentType,
		ETag:           etag,
		StorageBackend: "fs",
		LastModified:   time.Now(),
	}, nil
}

// Open returns an *os.File for the object, which supports Reader, ReaderAt,
// Seeker and Closer. Range reads are served directly from the file without
// loading the whole object into memory.
func (f *FS) Open(ctx context.Context, bucket, key string) (ObjectReader, error) {
	p := f.path(bucket, key)
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	return fh, nil
}

// Head returns metadata for the object by statting the file.
func (f *FS) Head(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	p := f.path(bucket, key)
	fi, err := os.Stat(p)
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{
		Key:            key,
		Size:           fi.Size(),
		StorageBackend: "fs",
		LastModified:   fi.ModTime(),
	}, nil
}

// Delete removes the object's file.
func (f *FS) Delete(ctx context.Context, bucket, key string) error {
	return os.Remove(f.path(bucket, key))
}

// Exists reports whether the object's file exists.
func (f *FS) Exists(ctx context.Context, bucket, key string) (bool, error) {
	_, err := os.Stat(f.path(bucket, key))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
