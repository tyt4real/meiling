package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"meiling/internal/ipfs"
)

// IPFS stores object content on IPFS and caches fetched objects locally so
// that range reads can be served without re-fetching from the network.
//
// Important: the IPFS HTTP API's /api/v0/cat does not support byte-range
// reads. The first read of an uncached object streams the entire object from
// IPFS into a local cache file; subsequent reads (including range reads) are
// served from that cache file. This behaviour is intentional and documented:
// range reads never claim to fetch only the requested bytes from IPFS.
type IPFS struct {
	mu      sync.Mutex
	client  *ipfs.NodeClient
	cacheDir string
	metaDir  string
}

// NewIPFS creates an IPFS-backed store. cacheDir is where fetched object
// content is cached locally. If cacheDir is empty, "ipfs-cache" is used. The
// cache is unbounded by default.
func NewIPFS(endpoint, cacheDir string) *IPFS {
	if cacheDir == "" {
		cacheDir = "ipfs-cache"
	}
	return &IPFS{
		client:   ipfs.NewNodeClient(endpoint),
		cacheDir: cacheDir,
		metaDir:  filepath.Join(cacheDir, "meta"),
	}
}

// contentPath returns the on-disk path for the cached content of an object.
func (s *IPFS) contentPath(bucket, key string) string {
	return filepath.Join(s.cacheDir, "buckets", bucket, key)
}

// metaPath returns the on-disk path for the CID sidecar of an object.
func (s *IPFS) metaPath(bucket, key string) string {
	return filepath.Join(s.metaDir, bucket, key+".json")
}

// objectMeta is the sidecar recording the CID for a bucket/key.
type objectMeta struct {
	CID           string    `json:"cid"`
	Size          int64     `json:"size"`
	ContentType   string    `json:"content_type,omitempty"`
	StorageBackend string   `json:"storage_backend"`
	CachedAt      time.Time `json:"cached_at"`
}

// Put streams src into IPFS, records the CID sidecar, and optionally caches
// the content locally. The whole body is streamed, not buffered in RAM.
func (s *IPFS) Put(ctx context.Context, bucket, key string, src io.Reader, contentType string) (ObjectInfo, error) {
	cid, size, err := s.client.Add(src)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("ipfs add: %w", err)
	}

	if err := s.writeMeta(bucket, key, objectMeta{
		CID:           cid,
		Size:          size,
		ContentType:   contentType,
		StorageBackend: "ipfs",
		CachedAt:      time.Now(),
	}); err != nil {
		slog.Warn("ipfs put: failed to write meta sidecar", "bucket", bucket, "key", key, "error", err)
	}

	info := ObjectInfo{
		Key:            key,
		Size:           size,
		ContentType:    contentType,
		ETag:           cid, // for IPFS objects the CID is used as ETag
		CID:            cid,
		StorageBackend: "ipfs",
		LastModified:   time.Now(),
	}
	return info, nil
}

// writeMeta persists the CID sidecar for an object.
func (s *IPFS) writeMeta(bucket, key string, m objectMeta) error {
	p := s.metaPath(bucket, key)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0644)
}

// readMeta loads the CID sidecar for an object.
func (s *IPFS) readMeta(bucket, key string) (objectMeta, error) {
	var m objectMeta
	b, err := os.ReadFile(s.metaPath(bucket, key))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

// Open returns a reader for the object. On the first read the content is
// streamed from IPFS into the local cache; subsequent reads use the cache,
// allowing efficient range reads via ReaderAt/Seeker.
func (s *IPFS) Open(ctx context.Context, bucket, key string) (ObjectReader, error) {
	m, err := s.readMeta(bucket, key)
	if err != nil {
		return nil, err
	}

	contentPath := s.contentPath(bucket, key)
	if _, err := os.Stat(contentPath); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := s.fetchToCache(ctx, bucket, key, m.CID, contentPath); err != nil {
			return nil, err
		}
	}

	fh, err := os.Open(contentPath)
	if err != nil {
		return nil, err
	}
	return fh, nil
}

// fetchToCache streams the CID from IPFS into a temp file then renames it into
// place so concurrent readers never observe a partially-written cache file.
func (s *IPFS) fetchToCache(ctx context.Context, bucket, key, cid, contentPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-check after acquiring the lock: another goroutine may have fetched it.
	if _, err := os.Stat(contentPath); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(contentPath), 0755); err != nil {
		return err
	}

	rc, err := s.client.Cat(cid)
	if err != nil {
		return fmt.Errorf("ipfs cat %s: %w", cid, err)
	}
	defer rc.Close()

	tmp := contentPath + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer out.Close()
	defer os.Remove(tmp)

	if _, err := io.Copy(out, rc); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, contentPath)
}

// Head returns metadata from the CID sidecar.
func (s *IPFS) Head(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	m, err := s.readMeta(bucket, key)
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{
		Key:            key,
		Size:           m.Size,
		ContentType:    m.ContentType,
		ETag:           m.CID,
		CID:            m.CID,
		StorageBackend: m.StorageBackend,
		LastModified:   m.CachedAt,
	}, nil
}

// Delete removes the cached content and meta sidecar. It does not unpin the
// CID from IPFS; shared-CID retention is handled by the pinning layer.
func (s *IPFS) Delete(ctx context.Context, bucket, key string) error {
	_ = os.Remove(s.contentPath(bucket, key))
	_ = os.Remove(s.contentPath(bucket, key) + ".tmp")
	_ = os.Remove(s.metaPath(bucket, key))
	return nil
}

// Exists reports whether the meta sidecar (and thus the object) is known.
func (s *IPFS) Exists(ctx context.Context, bucket, key string) (bool, error) {
	_, err := os.Stat(s.metaPath(bucket, key))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
