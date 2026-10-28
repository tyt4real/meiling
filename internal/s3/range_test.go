package s3_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"meiling/internal/db"
)

func doRangeReq(t *testing.T, server *httptest.Server, ak, sk, bucket, key string, rangeHeader string) *http.Response {
	t.Helper()
	var body io.Reader = nil
	req, err := sigv4Request(t, ak, sk, "GET", "/"+bucket+"/"+key, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	return doReq(t, server, req)
}

func putRangeObj(t *testing.T, server *httptest.Server, ak, sk, bucket, key, content string) {
	t.Helper()
	req, err := sigv4Request(t, ak, sk, "PUT", "/"+bucket+"/"+key, strings.NewReader(content), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp := doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put %s: %d", key, resp.StatusCode)
	}
	resp.Body.Close()
}

func TestGetObjectByteRanges(t *testing.T) {
	server := buildServer()
	defer server.Close()

	acc, err := db.CreateAccount(context.Background(), s3testPool, "range-"+uuid.New().String()[:8]+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bucket := "range-bucket-" + uuid.New().String()[:8]
	b, err := db.CreateBucket(context.Background(), s3testPool, acc.ID, bucket)
	if err != nil {
		t.Fatal(err)
	}
	ak := "RANGEAK" + uuid.New().String()[:8]
	sk := "RANGESK" + uuid.New().String()[:8]
	if _, err := db.CreateAccessKey(context.Background(), s3testPool, acc.ID, &b.ID, ak, sk); err != nil {
		t.Fatal(err)
	}

	content := strings.Repeat("abcdefghij", 100) // 1000 bytes
	putRangeObj(t, server, ak, sk, bucket, "big.txt", content)
	putRangeObj(t, server, ak, sk, bucket, "one.txt", "x")             // 1 byte
	putRangeObj(t, server, ak, sk, bucket, "empty.txt", "")            // 0 bytes

	// 1. No Range header → 200, full body.
	resp := doRangeReq(t, server, ak, sk, bucket, "big.txt", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no range: expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Error("expected Accept-Ranges: bytes")
	}
	if resp.Header.Get("Content-Length") != "1000" {
		t.Errorf("expected Content-Length 1000, got %s", resp.Header.Get("Content-Length"))
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(data) != content {
		t.Error("no range: body mismatch")
	}

	// 2. bytes=0-0 → 206, first byte.
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=0-0")
	checkRange(t, resp, http.StatusPartialContent, "bytes 0-0/1000", "1", string(content[0:1]))

	// 3. bytes=0-1023 → 206, first 1024 bytes (clamped to 1000).
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=0-1023")
	checkRange(t, resp, http.StatusPartialContent, "bytes 0-999/1000", "1000", content)

	// 4. bytes=1024- → start beyond EOF → 416.
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=1024-")
	checkRange(t, resp, http.StatusRequestedRangeNotSatisfiable, "bytes */1000", "", "")

	// 5. bytes=500- → 206, from 500 to end.
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=500-")
	checkRange(t, resp, http.StatusPartialContent, "bytes 500-999/1000", "500", content[500:])

	// 6. Suffix bytes=-50 → 206, last 50 bytes.
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=-50")
	checkRange(t, resp, http.StatusPartialContent, "bytes 950-999/1000", "50", content[950:])

	// 7. Range ending at final byte.
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=990-999")
	checkRange(t, resp, http.StatusPartialContent, "bytes 990-999/1000", "10", content[990:])

	// 8. Range extending beyond EOF → 206, clamped.
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=900-2000")
	checkRange(t, resp, http.StatusPartialContent, "bytes 900-999/1000", "100", content[900:])

	// 9. Empty object, any range → 416, bytes */0.
	resp = doRangeReq(t, server, ak, sk, bucket, "empty.txt", "bytes=0-0")
	checkRange(t, resp, http.StatusRequestedRangeNotSatisfiable, "bytes */0", "", "")

	// 10. One-byte object, bytes=0-0 → 206.
	resp = doRangeReq(t, server, ak, sk, bucket, "one.txt", "bytes=0-0")
	checkRange(t, resp, http.StatusPartialContent, "bytes 0-0/1", "1", "x")

	// 11. Unsatisfiable: bytes=5-0 (start > end) → 416? Spec: start>end unsatisfiable.
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=5-0")
	checkRange(t, resp, http.StatusRequestedRangeNotSatisfiable, "bytes */1000", "", "")

	// 12. Malformed range → 200 (S3 treats invalid Range as no range).
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=abc-def")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("malformed range: expected 200, got %d", resp.StatusCode)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// 13. Multiple ranges → 200 (not implemented as multipart).
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=0-10,20-30")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("multiple ranges: expected 200, got %d", resp.StatusCode)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// 14. Negative-ish (non-numeric) → 200.
	resp = doRangeReq(t, server, ak, sk, bucket, "big.txt", "bytes=-abc")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("invalid suffix: expected 200, got %d", resp.StatusCode)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// 15. HEAD returns Accept-Ranges and correct size.
	req, err := sigv4Request(t, ak, sk, "HEAD", "/"+bucket+"/big.txt", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, server, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("head: %d", resp.StatusCode)
	}
	if resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Error("head: expected Accept-Ranges: bytes")
	}
	if resp.Header.Get("Content-Length") != "1000" {
		t.Errorf("head: expected Content-Length 1000, got %s", resp.Header.Get("Content-Length"))
	}
	resp.Body.Close()
}

func checkRange(t *testing.T, resp *http.Response, wantStatus int, wantCR, wantLen, wantBody string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Errorf("expected %d, got %d (body: %q)", wantStatus, resp.StatusCode, resp.Header.Get("Content-Range"))
	}
	if got := resp.Header.Get("Content-Range"); got != wantCR {
		t.Errorf("expected Content-Range %q, got %q", wantCR, got)
	}
	if wantLen != "" {
		if got := resp.Header.Get("Content-Length"); got != wantLen {
			t.Errorf("expected Content-Length %q, got %q", wantLen, got)
		}
	}
	if resp.StatusCode == http.StatusPartialContent && wantBody != "" {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(data) != wantBody {
			t.Errorf("expected body %q, got %q", wantBody, string(data))
		}
	} else {
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
}
