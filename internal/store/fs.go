package store

import (
	"io"
	"os"
	"path/filepath"
)

type FS struct {
	Root string
}

func (f *FS) path(bucket, key string) string {
	return filepath.Join(f.Root, "buckets", bucket, key)
}

func (f *FS) Put(bucket, key string, data io.Reader) (int64, error) {
	p := f.path(bucket, key)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return 0, err
	}
	out, err := os.Create(p)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, data)
}

func (f *FS) Get(bucket, key string) (io.ReadCloser, int64, error) {
	p := f.path(bucket, key)
	fi, err := os.Stat(p)
	if err != nil {
		return nil, 0, err
	}
	fh, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	return fh, fi.Size(), nil
}

func (f *FS) Delete(bucket, key string) error {
	return os.Remove(f.path(bucket, key))
}

func (f *FS) Exists(bucket, key string) bool {
	_, err := os.Stat(f.path(bucket, key))
	return err == nil
}
