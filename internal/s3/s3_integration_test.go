package s3_test

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

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

var s3testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	var err error
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		dbURL = "postgres://s3ipfs:s3ipfs@localhost:5432/s3ipfs?sslmode=disable"
	}
	s3testPool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		panic(fmt.Sprintf("connect test db: %v", err))
	}
	defer s3testPool.Close()

	const schema = `
CREATE TABLE IF NOT EXISTS accounts (id UUID PRIMARY KEY, email TEXT UNIQUE NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS buckets (id UUID PRIMARY KEY, account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE, name TEXT UNIQUE NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS access_keys (id UUID PRIMARY KEY, account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE, bucket_id UUID REFERENCES buckets(id) ON DELETE CASCADE, access_key_id TEXT UNIQUE NOT NULL, secret_access_key TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS objects (id UUID PRIMARY KEY, bucket_id UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE, key TEXT NOT NULL, data_path TEXT NOT NULL, size BIGINT NOT NULL, content_type TEXT, etag TEXT NOT NULL, cid TEXT, ipfs_status TEXT NOT NULL DEFAULT 'none', user_metadata JSONB NOT NULL DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), UNIQUE (bucket_id, key));
CREATE TABLE IF NOT EXISTS ipfs_nodes (id UUID PRIMARY KEY, peer_id TEXT, api_endpoint TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'healthy', last_seen TIMESTAMPTZ, capacity_used BIGINT NOT NULL DEFAULT 0, capacity_total BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS ipfs_pins (id UUID PRIMARY KEY, bucket_id UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE, request_id TEXT UNIQUE NOT NULL, cid TEXT NOT NULL, name TEXT, status TEXT NOT NULL DEFAULT 'queued', size TEXT NOT NULL DEFAULT '0', replica_factor INT NOT NULL DEFAULT 2, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS ipfs_pin_replicas (id UUID PRIMARY KEY, pin_id UUID NOT NULL REFERENCES ipfs_pins(id) ON DELETE CASCADE, node_id UUID NOT NULL REFERENCES ipfs_nodes(id) ON DELETE CASCADE, status TEXT NOT NULL DEFAULT 'queued', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), UNIQUE (pin_id, node_id));
CREATE TABLE IF NOT EXISTS multipart_uploads (id UUID PRIMARY KEY, upload_id TEXT UNIQUE NOT NULL, bucket_id UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE, key TEXT NOT NULL, content_type TEXT, user_metadata JSONB NOT NULL DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS multipart_parts (upload_id TEXT NOT NULL REFERENCES multipart_uploads(upload_id) ON DELETE CASCADE, part_number INT NOT NULL, size BIGINT NOT NULL, etag TEXT NOT NULL, PRIMARY KEY (upload_id, part_number));
`
	if _, err := s3testPool.Exec(context.Background(), schema); err != nil {
		panic(fmt.Sprintf("migrate: %v", err))
	}
	if _, err := db.CreateNode(context.Background(), s3testPool, "http://localhost:1", ""); err != nil {
		panic(fmt.Sprintf("create node: %v", err))
	}

	code := m.Run()
	os.Exit(code)
}

func buildServer() *httptest.Server {
	st := &store.FS{Root: "/tmp/s3ipfs-test-storage-external"}
	reg := metrics.NewRegistry()
	s3Handler := s3.S3Handler(s3testPool, st, "/tmp/s3ipfs-test-storage-external", nil, reg)
	dist := ipfs.NewDistributor(s3testPool, reg)
	ipfsHandler := ipfs.IPFSHandler(s3testPool, dist, reg)

	authMW := &auth.Middleware{
		Next: s3Handler,
		GetSecret: func(accessKeyID string) (string, error) {
			k, err := db.GetAccessKey(context.Background(), s3testPool, accessKeyID)
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

func sigv4Request(t *testing.T, accessKey, secretKey, method, path string, body io.Reader, query url.Values) (*http.Request, error) {
	t.Helper()
	now := time.Now().UTC()
	amz := now.Format("20060102T150405Z")
	date := now.Format("20060102")

	// Read body before constructing the request so it isn't consumed.
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = io.ReadAll(body)
	}
	body = strings.NewReader(string(bodyBytes))

	sum := sha256.Sum256(bodyBytes)
	payloadHash := hex.EncodeToString(sum[:])

	req, err := http.NewRequest(method, "http://x"+path, body)
	if err != nil {
		return nil, err
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	req.Host = "localhost:8080"

	canonicalHeaders := fmt.Sprintf("host:localhost:8080\nx-amz-content-sha256:%s\nx-amz-date:%s\n", payloadHash, amz)
	canonicalQuery := urlencodeSorted(query)
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\nhost;x-amz-content-sha256;x-amz-date\n%s",
		method, path, canonicalQuery, canonicalHeaders, payloadHash)
	sum2 := sha256.Sum256([]byte(canonical))
	hashedCanonical := hex.EncodeToString(sum2[:])
	credential := accessKey + "/" + date + "/auto/s3/aws4_request"
	sts := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s/%s/%s/%s\n%s", amz, date, "auto", "s3", "aws4_request", hashedCanonical)

	signingKey := auth.SigningKey(secretKey, date, "auto", "s3")
	signature := auth.Signature(signingKey, sts)

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=%s", credential, signature)
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Date", amz)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	return req, nil
}

func urlencodeSorted(v url.Values) string {
	if v == nil {
		return ""
	}
	var keys []string
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v.Get(k)))
	}
	return strings.Join(parts, "&")
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

func TestS3FullFlow(t *testing.T) {
	server := buildServer()
	defer server.Close()

	acc, err := db.CreateAccount(context.Background(), s3testPool, "flow-"+uuid.New().String()[:8]+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bucketName := "test-bucket-" + uuid.New().String()[:8]
	b, err := db.CreateBucket(context.Background(), s3testPool, acc.ID, bucketName)
	if err != nil {
		t.Fatal(err)
	}
	ak := "FLOWKEY" + uuid.New().String()[:8]
	sk := "FLOWSECRET" + uuid.New().String()[:8]
	if _, err := db.CreateAccessKey(context.Background(), s3testPool, acc.ID, &b.ID, ak, sk); err != nil {
		t.Fatal(err)
	}

	// List buckets.
	req, err := sigv4Request(t, ak, sk, "GET", "/", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list buckets: %d", resp.StatusCode)
	}

	// Put object.
	putBody := strings.NewReader("hello world")
	req, err = sigv4Request(t, ak, sk, "PUT", "/"+bucketName+"/hello.txt", putBody, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put object: %d", resp.StatusCode)
	}

	// Get object.
	req, err = sigv4Request(t, ak, sk, "GET", "/"+bucketName+"/hello.txt", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get object: %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %s", data)
	}

	// Head object.
	req, err = sigv4Request(t, ak, sk, "HEAD", "/"+bucketName+"/hello.txt", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("head object: %d", resp.StatusCode)
	}

	// List objects.
	req, err = sigv4Request(t, ak, sk, "GET", "/"+bucketName, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list objects: %d", resp.StatusCode)
	}
	listBody, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(listBody), "hello.txt") {
		t.Error("list should contain hello.txt")
	}

	// Delete object.
	req, err = sigv4Request(t, ak, sk, "DELETE", "/"+bucketName+"/hello.txt", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete object: %d", resp.StatusCode)
	}

	// Delete bucket.
	req, err = sigv4Request(t, ak, sk, "DELETE", "/"+bucketName, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete bucket: %d", resp.StatusCode)
	}
}

func multipartCompleteXML(parts []struct {
	PartNumber int
	ETag       string
}) string {
	var b strings.Builder
	b.WriteString(`<CompleteMultipartUpload>`)
	for _, p := range parts {
		b.WriteString(fmt.Sprintf(`<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>`, p.PartNumber, p.ETag))
	}
	b.WriteString(`</CompleteMultipartUpload>`)
	return b.String()
}

func uploadPart(t *testing.T, server *httptest.Server, ak, sk, bucket, key, uploadID string, partNumber int, content string) string {
	t.Helper()
	query := url.Values{"partNumber": {fmt.Sprint(partNumber)}, "uploadId": {uploadID}}
	req, err := sigv4Request(t, ak, sk, "PUT", "/"+bucket+"/"+key, strings.NewReader(content), query)
	if err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload part %d: %d", partNumber, resp.StatusCode)
	}
	etag := strings.Trim(resp.Header.Get("ETag"), "\"")
	resp.Body.Close()
	return etag
}

func TestS3MultipartUploadComplete(t *testing.T) {
	server := buildServer()
	defer server.Close()

	acc, err := db.CreateAccount(context.Background(), s3testPool, "mp-"+uuid.New().String()[:8]+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bucket := "mp-bucket-" + uuid.New().String()[:8]
	b, err := db.CreateBucket(context.Background(), s3testPool, acc.ID, bucket)
	if err != nil {
		t.Fatal(err)
	}
	ak := "MPAK" + uuid.New().String()[:8]
	sk := "MPSK" + uuid.New().String()[:8]
	if _, err := db.CreateAccessKey(context.Background(), s3testPool, acc.ID, &b.ID, ak, sk); err != nil {
		t.Fatal(err)
	}

	key := "multipart.bin"
	part1 := strings.Repeat("aaa", 100)   // 300 bytes
	part2 := strings.Repeat("bbb", 100)   // 300 bytes
	expected := part1 + part2

	// Initiate multipart upload.
	req, err := sigv4Request(t, ak, sk, "POST", "/"+bucket+"/"+key, nil, url.Values{"uploads": {""}})
	if err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create multipart upload: %d", resp.StatusCode)
	}
	var initResp struct {
		UploadId string `xml:"UploadId"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if initResp.UploadId == "" {
		t.Fatal("expected upload id")
	}

	// Upload parts.
	e1 := uploadPart(t, server, ak, sk, bucket, key, initResp.UploadId, 1, part1)
	e2 := uploadPart(t, server, ak, sk, bucket, key, initResp.UploadId, 2, part2)

	// Complete multipart upload.
	body := strings.NewReader(multipartCompleteXML([]struct {
		PartNumber int
		ETag       string
	}{{1, e1}, {2, e2}}))
	req, err = sigv4Request(t, ak, sk, "POST", "/"+bucket+"/"+key, body, url.Values{"uploadId": {initResp.UploadId}})
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/xml")
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete multipart: %d", resp.StatusCode)
	}
	var compResp struct {
		ETag string `xml:"ETag"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&compResp); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	expectedEtag := md5Sum(e1+e2) + "-2"
	if compResp.ETag != expectedEtag {
		t.Errorf("expected ETag %s, got %s", expectedEtag, compResp.ETag)
	}

	// Get object: content must match concatenation.
	req, err = sigv4Request(t, ak, sk, "GET", "/"+bucket+"/"+key, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get object: %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(data) != expected {
		t.Errorf("content mismatch: got %q", string(data))
	}
}

func TestS3MultipartUploadAbort(t *testing.T) {
	server := buildServer()
	defer server.Close()

	acc, err := db.CreateAccount(context.Background(), s3testPool, "mpa-"+uuid.New().String()[:8]+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bucket := "mpa-bucket-" + uuid.New().String()[:8]
	b, err := db.CreateBucket(context.Background(), s3testPool, acc.ID, bucket)
	if err != nil {
		t.Fatal(err)
	}
	ak := "MPAAK" + uuid.New().String()[:8]
	sk := "MPASK" + uuid.New().String()[:8]
	if _, err := db.CreateAccessKey(context.Background(), s3testPool, acc.ID, &b.ID, ak, sk); err != nil {
		t.Fatal(err)
	}

	key := "aborted.bin"

	// Initiate.
	req, err := sigv4Request(t, ak, sk, "POST", "/"+bucket+"/"+key, nil, url.Values{"uploads": {""}})
	if err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, server, req)
	var initResp struct {
		UploadId string `xml:"UploadId"`
	}
	_ = xml.NewDecoder(resp.Body).Decode(&initResp)
	resp.Body.Close()

	// Upload a part.
	_ = uploadPart(t, server, ak, sk, bucket, key, initResp.UploadId, 1, "part data")

	// Abort.
	req, err = sigv4Request(t, ak, sk, "DELETE", "/"+bucket+"/"+key, nil, url.Values{"uploadId": {initResp.UploadId}})
	if err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("abort multipart: %d", resp.StatusCode)
	}

	// Completing the aborted upload should fail.
	body := strings.NewReader(multipartCompleteXML([]struct {
		PartNumber int
		ETag       string
	}{{1, "whatever"}}))
	req, err = sigv4Request(t, ak, sk, "POST", "/"+bucket+"/"+key, body, url.Values{"uploadId": {initResp.UploadId}})
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/xml")
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after abort, got %d", resp.StatusCode)
	}
}

func md5Sum(s string) string {
	h := md5.New()
	io.WriteString(h, s)
	return fmt.Sprintf("%x", h.Sum(nil))
}

