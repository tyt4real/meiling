package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareServeHTTPValid(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := AccessKeyFromRequest(r)
		if !ok {
			http.Error(w, "no key", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok:" + v))
	})
	m := &Middleware{
		Next: inner,
	GetSecret: func(accessKeyID string) (string, error) {
		if accessKeyID == "goodkey" {
			return "goodsecret", nil
		}
		return "", nil
	},
	}

	// Build a valid signed request.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Amz-Date", "20260102T000000Z")
	req.Host = "localhost:8080"
	bodyHash := hexHash([]byte{})
	canonical := "GET\n/\n\nhost:localhost:8080\nx-amz-content-sha256:" + bodyHash + "\nx-amz-date:20260102T000000Z\n\nhost;x-amz-content-sha256;x-amz-date\n" + bodyHash
	hashedCanonical := hexHash([]byte(canonical))
	credential := "goodkey/20260102/auto/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n20260102T000000Z\n20260102/auto/s3/aws4_request\n" + hashedCanonical
	key := SigningKey("goodsecret", "20260102", "auto", "s3")
	signature := Signature(key, sts)
	authHeader := "AWS4-HMAC-SHA256 Credential=" + credential + ", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + signature
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Content-Sha256", bodyHash)

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.HasPrefix(rec.Body.String(), "ok:goodkey") {
		t.Errorf("expected access key in context, got %s", rec.Body.String())
	}
}

func TestMiddlewareServeHTTPInvalid(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	m := &Middleware{
		Next: inner,
		GetSecret: func(accessKeyID string) (string, error) {
			return "wrongsecret", nil
		},
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Amz-Date", "20260102T000000Z")
	req.Host = "localhost:8080"
	bodyHash := hexHash([]byte{})
	canonical := "GET\n/\n\nhost:localhost:8080\nx-amz-content-sha256:" + bodyHash + "\nx-amz-date:20260102T000000Z\n\nhost;x-amz-content-sha256;x-amz-date\n" + bodyHash
	hashedCanonical := hexHash([]byte(canonical))
	credential := "goodkey/20260102/auto/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n20260102T000000Z\n20260102/auto/s3/aws4_request\n" + hashedCanonical
	key := SigningKey("goodsecret", "20260102", "auto", "s3")
	signature := Signature(key, sts)
	authHeader := "AWS4-HMAC-SHA256 Credential=" + credential + ", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + signature
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Content-Sha256", bodyHash)

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 signature mismatch, got %d", rec.Code)
	}
}
