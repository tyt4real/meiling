package ipfs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/auth"
	"meiling/internal/db"
	"meiling/internal/ipfs"
	"meiling/internal/metrics"
	"meiling/internal/s3"
	"meiling/internal/security"
	"meiling/internal/store"
)

var ipfstestPool *pgxpool.Pool

func TestMain(m *testing.M) {
	var err error
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		dbURL = "postgres://s3ipfs:s3ipfs@localhost:5432/s3ipfs?sslmode=disable"
	}
	ipfstestPool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		panic(fmt.Sprintf("connect test db: %v", err))
	}
	defer ipfstestPool.Close()

	const schema = `
CREATE TABLE IF NOT EXISTS accounts (id UUID PRIMARY KEY, email TEXT UNIQUE NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS buckets (id UUID PRIMARY KEY, account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE, name TEXT UNIQUE NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS access_keys (id UUID PRIMARY KEY, account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE, bucket_id UUID REFERENCES buckets(id) ON DELETE CASCADE, access_key_id TEXT UNIQUE NOT NULL, secret_access_key TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS ipfs_nodes (id UUID PRIMARY KEY, peer_id TEXT, api_endpoint TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'healthy', last_seen TIMESTAMPTZ, capacity_used BIGINT NOT NULL DEFAULT 0, capacity_total BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS ipfs_pins (id UUID PRIMARY KEY, bucket_id UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE, request_id TEXT UNIQUE NOT NULL, cid TEXT NOT NULL, name TEXT, status TEXT NOT NULL DEFAULT 'queued', size TEXT NOT NULL DEFAULT '0', replica_factor INT NOT NULL DEFAULT 2, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS ipfs_pin_replicas (id UUID PRIMARY KEY, pin_id UUID NOT NULL REFERENCES ipfs_pins(id) ON DELETE CASCADE, node_id UUID NOT NULL REFERENCES ipfs_nodes(id) ON DELETE CASCADE, status TEXT NOT NULL DEFAULT 'queued', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), UNIQUE (pin_id, node_id));
`
	if _, err := ipfstestPool.Exec(context.Background(), schema); err != nil {
		panic(fmt.Sprintf("migrate: %v", err))
	}
	if _, err := db.CreateNode(context.Background(), ipfstestPool, "http://localhost:1", ""); err != nil {
		panic(fmt.Sprintf("create node: %v", err))
	}

	code := m.Run()
	os.Exit(code)
}

func buildServer() *httptest.Server {
	st := &store.FS{Root: "/tmp/s3ipfs-ipfs-test-storage-external"}
	reg := metrics.NewRegistry()
	s3Handler := s3.S3Handler(ipfstestPool, st, "/tmp/s3ipfs-ipfs-test-storage-external", nil, reg)
	dist := ipfs.NewDistributor(ipfstestPool, reg)
	ipfsHandler := ipfs.IPFSHandler(ipfstestPool, dist, reg)

	authMW := &auth.Middleware{
		Next: s3Handler,
		GetSecret: func(accessKeyID string) (string, error) {
			k, err := db.GetAccessKey(context.Background(), ipfstestPool, accessKeyID)
			if err != nil {
				return "", err
			}
			return k.SecretAccessKey, nil
		},
	}
	secMW := security.NewMiddleware(authMW, 1000, 1000, []string{"*"}, 1024, false)

	mux := http.NewServeMux()
	mux.Handle("/", secMW)
	mux.Handle("/ipfs/", ipfsHandler)
	return httptest.NewServer(mux)
}

func bearerReq(t *testing.T, token, method, path string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "http://x"+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func doReq(t *testing.T, server *httptest.Server, req *http.Request) *http.Response {
	t.Helper()
	req.URL.Scheme = "http"
	req.URL.Host = server.URL[7:]
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func TestIPFSFullFlow(t *testing.T) {
	server := buildServer()
	defer server.Close()

	acc, err := db.CreateAccount(context.Background(), ipfstestPool, "ipfs-"+uuid.New().String()[:8]+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateBucket(context.Background(), ipfstestPool, acc.ID, "ipfs-bucket-"+uuid.New().String()[:8])
	if err != nil {
		t.Fatal(err)
	}
	token := "ipfs-token-" + uuid.New().String()[:8]
	if _, err := db.CreateAccessKey(context.Background(), ipfstestPool, acc.ID, &b.ID, token, "unused"); err != nil {
		t.Fatal(err)
	}

	cid := "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"

	// Add pin.
	payload := strings.NewReader(`{"cid":"` + cid + `","name":"test.txt"}`)
	req := bearerReq(t, token, "POST", "/ipfs/pins", payload)
	req.Header.Set("Content-Type", "application/json")
	resp := doReq(t, server, req)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("add pin: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var addResp struct {
		RequestID string `json:"requestid"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(body, &addResp); err != nil {
		t.Fatal(err)
	}
	if addResp.RequestID == "" {
		t.Error("expected requestid")
	}

	// List pins.
	req = bearerReq(t, token, "GET", "/ipfs/pins", nil)
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list pins: %d", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), cid) {
		t.Error("list should contain the pinned CID")
	}

	// Get pin.
	req = bearerReq(t, token, "GET", "/ipfs/pins/"+addResp.RequestID, nil)
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get pin: %d", resp.StatusCode)
	}

	// Delete pin.
	req = bearerReq(t, token, "DELETE", "/ipfs/pins/"+addResp.RequestID, nil)
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("delete pin: %d", resp.StatusCode)
	}

	// Get deleted pin -> 404.
	req = bearerReq(t, token, "GET", "/ipfs/pins/"+addResp.RequestID, nil)
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("deleted pin should be 404, got %d", resp.StatusCode)
	}
}
