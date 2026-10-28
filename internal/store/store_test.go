package store

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFSPutGetDelete(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Root: dir}

	// Put
	info, err := fs.Put(context.Background(), "bucket1", "key1.txt", strings.NewReader("hello world"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 11 {
		t.Errorf("expected 11 bytes, got %d", info.Size)
	}

	// Exists
	if ok, _ := fs.Exists(context.Background(), "bucket1", "key1.txt"); !ok {
		t.Error("expected key to exist")
	}
	if ok, _ := fs.Exists(context.Background(), "bucket1", "missing.txt"); ok {
		t.Error("expected missing key to not exist")
	}

	// Get
	rc, err := fs.Open(context.Background(), "bucket1", "key1.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %s", data)
	}

	// Delete
	if err := fs.Delete(context.Background(), "bucket1", "key1.txt"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := fs.Exists(context.Background(), "bucket1", "key1.txt"); ok {
		t.Error("expected key to be deleted")
	}

	// Nested key
	nested := "a/b/c/deep.txt"
	if _, err := fs.Put(context.Background(), "bucket1", nested, strings.NewReader("deep"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	rc, err = fs.Open(context.Background(), "bucket1", nested)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	_ = os.Remove(filepath.Join(dir, "bucket1", "a", "b", "c", "deep.txt"))
}

func TestFSRangeReads(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Root: dir}
	content := strings.Repeat("abcdefghij", 100) // 1000 bytes
	if _, err := fs.Put(context.Background(), "bucket1", "big.txt", strings.NewReader(content), "text/plain"); err != nil {
		t.Fatal(err)
	}

	// ReaderAt: read bytes 100-199.
	rc, err := fs.Open(context.Background(), "bucket1", "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	buf := make([]byte, 100)
	if _, err := rc.ReadAt(buf, 100); err != nil {
		t.Fatal(err)
	}
	if string(buf) != content[100:200] {
		t.Errorf("ReadAt range mismatch: %q", string(buf))
	}

	// Seek to 900 and read the rest.
	if _, err := rc.Seek(900, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(rc)
	if string(rest) != content[900:] {
		t.Errorf("Seek range mismatch: %q", string(rest))
	}

	// Head reports size.
	info, err := fs.Head(context.Background(), "bucket1", "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), info.Size)
	}
}

func TestFSPutGetDeleteNoBuffer(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Root: dir}
	failing := &failingReader{data: []byte("some data"), failAfter: 3}
	if _, err := fs.Put(context.Background(), "bucket1", "bad.txt", failing, "text/plain"); err == nil {
		t.Error("expected put to fail mid-stream")
	}
	if ok, _ := fs.Exists(context.Background(), "bucket1", "bad.txt"); ok {
		t.Error("expected failed put to not leave object behind")
	}
}

type failingReader struct {
	data      []byte
	failAfter int
	pos       int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.pos >= r.failAfter {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
