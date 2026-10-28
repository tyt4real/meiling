package store

import (
	"context"
	"io"
	"strings"
	"testing"
)

// countingReader wraps an io.Reader and counts bytes read.
type countingReader struct {
	src   io.Reader
	n     int64
	reads int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	r.n += int64(n)
	r.reads++
	return n, err
}

func BenchmarkFSRangeRead(b *testing.B) {
	dir := b.TempDir()
	fs := &FS{Root: dir}
	content := strings.Repeat("benchmark-data-", 100000) // ~2.1 MB
	if _, err := fs.Put(context.Background(), "bucket1", "big.txt", strings.NewReader(content), "text/plain"); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rc, err := fs.Open(context.Background(), "bucket1", "big.txt")
		if err != nil {
			b.Fatal(err)
		}
		buf := make([]byte, 4096)
		if _, err := rc.ReadAt(buf, 1024*1024); err != nil {
			rc.Close()
			b.Fatal(err)
		}
		rc.Close()
	}
	b.SetBytes(4096)
}

func BenchmarkFSSequentialRead(b *testing.B) {
	dir := b.TempDir()
	fs := &FS{Root: dir}
	content := strings.Repeat("seq-data-", 100000)
	if _, err := fs.Put(context.Background(), "bucket1", "big.txt", strings.NewReader(content), "text/plain"); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rc, err := fs.Open(context.Background(), "bucket1", "big.txt")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			rc.Close()
			b.Fatal(err)
		}
		rc.Close()
	}
	b.SetBytes(int64(len(content)))
}
