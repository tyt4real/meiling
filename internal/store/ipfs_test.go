package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockIPFS emulates a subset of the IPFS HTTP API: /api/v0/add and /api/v0/cat.
type mockIPFS struct {
	mu     map[string][]byte // cid -> content
	contents map[string][]byte
}

func newMockIPFS() *mockIPFS {
	return &mockIPFS{contents: make(map[string][]byte)}
}

func (m *mockIPFS) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v0/add":
		ct := r.Header.Get("Content-Type")
		if ct == "" || !strings.HasPrefix(ct, "multipart/form-data") {
			http.Error(w, "expected multipart/form-data", http.StatusBadRequest)
			return
		}
		// Stream the multipart body and pick up the "file" field content.
		mr := multipart.NewReader(r.Body, strings.TrimPrefix(ct, "multipart/form-data; boundary="))
		var fileData []byte
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if part.FormName() == "file" {
				fileData, err = io.ReadAll(part)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
		sum := sha256.Sum256(fileData)
		cid := "bafy" + hex.EncodeToString(sum[:])
		m.contents[cid] = fileData
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"Hash": cid, "Size": int64(len(fileData))})
	case "/api/v0/cat":
		cid := r.URL.Query().Get("arg")
		data, ok := m.contents[cid]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(data)
	default:
		http.Error(w, "unknown path", http.StatusNotFound)
	}
}

func TestIPFSPutGetDelete(t *testing.T) {
	mock := newMockIPFS()
	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	defer server.Close()

	dir := t.TempDir()
	st := NewIPFS(server.URL, dir)

	content := []byte("hello ipfs world")
	info, err := st.Put(context.Background(), "bucket1", "key1.txt", strings.NewReader(string(content)), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if info.CID == "" {
		t.Error("expected CID after put")
	}
	if info.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), info.Size)
	}
	if info.StorageBackend != "ipfs" {
		t.Errorf("expected ipfs backend, got %s", info.StorageBackend)
	}

	// Exists
	ok, err := st.Exists(context.Background(), "bucket1", "key1.txt")
	if err != nil || !ok {
		t.Errorf("expected exists=true, got %v %v", ok, err)
	}

	// Head
	head, err := st.Head(context.Background(), "bucket1", "key1.txt")
	if err != nil {
		t.Fatal(err)
	}
	if head.CID != info.CID {
		t.Errorf("head cid mismatch: %s vs %s", head.CID, info.CID)
	}

	// Open and read full content.
	rc, err := st.Open(context.Background(), "bucket1", "key1.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch: %q", string(data))
	}

	// Delete
	if err := st.Delete(context.Background(), "bucket1", "key1.txt"); err != nil {
		t.Fatal(err)
	}
	ok, _ = st.Exists(context.Background(), "bucket1", "key1.txt")
	if ok {
		t.Error("expected object to be deleted")
	}
}

func TestIPFSRangeReads(t *testing.T) {
	mock := newMockIPFS()
	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	defer server.Close()

	dir := t.TempDir()
	st := NewIPFS(server.URL, dir)

	content := strings.Repeat("0123456789", 100) // 1000 bytes
	if _, err := st.Put(context.Background(), "bucket1", "big.txt", strings.NewReader(content), "text/plain"); err != nil {
		t.Fatal(err)
	}

	// First open triggers a full fetch from IPFS into the cache.
	rc, err := st.Open(context.Background(), "bucket1", "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	// ReaderAt range read from the cache.
	buf := make([]byte, 50)
	if _, err := rc.ReadAt(buf, 100); err != nil {
		t.Fatal(err)
	}
	if string(buf) != content[100:150] {
		t.Errorf("ReadAt mismatch: %q", string(buf))
	}

	// Seek + read suffix.
	if _, err := rc.Seek(950, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(rc)
	if string(rest) != content[950:] {
		t.Errorf("Seek mismatch: %q", string(rest))
	}
}
