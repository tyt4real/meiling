package presign

import (
	"net/url"
	"strings"
	"testing"
)

func TestGetObjectReturnsSignedURL(t *testing.T) {
	u := GetObject("AKIA", "secret", "auto", "bucket", "key", 0)
	if u == "" {
		t.Fatal("expected non-empty URL")
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("invalid URL: %v", err)
	}
	q := parsed.Query()
	if q.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
		t.Error("missing algorithm")
	}
	if q.Get("X-Amz-Credential") == "" {
		t.Error("missing credential")
	}
	if q.Get("X-Amz-Signature") == "" {
		t.Error("missing signature")
	}
	if !strings.Contains(parsed.Path, "/bucket/key") {
		t.Error("path should contain bucket/key")
	}
}

func TestPutObjectReturnsSignedURL(t *testing.T) {
	u := PutObject("AKIA", "secret", "auto", "bucket", "key", 0)
	if u == "" {
		t.Fatal("expected non-empty URL")
	}
	parsed, _ := url.Parse(u)
	q := parsed.Query()
	if q.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
		t.Error("missing algorithm")
	}
	if q.Get("X-Amz-Signature") == "" {
		t.Error("missing signature")
	}
}
