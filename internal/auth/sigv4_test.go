package auth

import (
	"net/http/httptest"
	"testing"
)

func TestParseAuthExtractsCredential(t *testing.T) {
	req := httptest.NewRequest("GET", "/bucket/key", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIA/20260102/auto/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=deadbeef")
	req.Header.Set("X-Amz-Date", "20260102T000000Z")
	req.Host = "localhost:8080"

	info, err := ParseAuth(req)
	if err != nil {
		t.Fatalf("ParseAuth error: %v", err)
	}
	if info.Credential.AccessKeyID != "AKIA" {
		t.Errorf("expected AKIA, got %s", info.Credential.AccessKeyID)
	}
	if info.Credential.Region != "auto" {
		t.Errorf("expected auto, got %s", info.Credential.Region)
	}
	if info.Signature != "deadbeef" {
		t.Errorf("expected deadbeef, got %s", info.Signature)
	}
}

func TestParseAuthMissing(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	_, err := ParseAuth(req)
	if err == nil {
		t.Error("expected error for missing Authorization")
	}
}

func TestValidateTimingSafe(t *testing.T) {
	info := &AuthInfo{
		Credential: Credential{
			AccessKeyID: "k",
			Date:        "20260102",
			Region:      "auto",
			Service:     "s3",
			Request:     "aws4_request",
		},
		Signature:     "aa",
		SignedHeaders: []string{"host"},
		Headers:       map[string]string{},
		Method:        "GET",
		URI:           "/",
		Query:         "",
		BodyHash:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}
	// wrong secret -> invalid
	if Validate(info, "wrong") {
		t.Error("expected invalid for wrong secret")
	}
	// Compute correct secret
	key := SigningKey("secret", info.Credential.Date, info.Credential.Region, info.Credential.Service)
	canonicalHash := hexHash([]byte(info.CanonicalRequest()))
	sts := info.StringToSign(canonicalHash)
	correctSig := Signature(key, sts)
	info.Signature = correctSig
	if !Validate(info, "secret") {
		t.Error("expected valid for correct signature")
	}
}
