package store

import (
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
	n, err := fs.Put("bucket1", "key1.txt", strings.NewReader("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Errorf("expected 11 bytes, got %d", n)
	}

	// Exists
	if !fs.Exists("bucket1", "key1.txt") {
		t.Error("expected key to exist")
	}
	if fs.Exists("bucket1", "missing.txt") {
		t.Error("expected missing key to not exist")
	}

	// Get
	rc, size, err := fs.Get("bucket1", "key1.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if size != 11 {
		t.Errorf("expected size 11, got %d", size)
	}
	data, _ := io.ReadAll(rc)
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %s", data)
	}

	// Delete
	if err := fs.Delete("bucket1", "key1.txt"); err != nil {
		t.Fatal(err)
	}
	if fs.Exists("bucket1", "key1.txt") {
		t.Error("expected key to be deleted")
	}

	// Nested key
	nested := "a/b/c/deep.txt"
	if _, err := fs.Put("bucket1", nested, strings.NewReader("deep")); err != nil {
		t.Fatal(err)
	}
	rc, _, err = fs.Get("bucket1", nested)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	_ = os.Remove(filepath.Join(dir, "bucket1", "a", "b", "c", "deep.txt"))
}
