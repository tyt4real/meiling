package validation

import (
	"strings"
	"testing"
)

func TestValidateObjectKeyOK(t *testing.T) {
	for _, k := range []string{"file.txt", "a/b/c.txt", "foo/bar/baz"} {
		if err := ValidateObjectKey(k); err != nil {
			t.Errorf("key %q should be valid: %v", k, err)
		}
	}
}

func TestValidateObjectKeyReject(t *testing.T) {
	bad := []string{"", "/abs.txt", "a/../b.txt", "a\x00b"}
	for _, k := range bad {
		if err := ValidateObjectKey(k); err == nil {
			t.Errorf("key %q should be invalid", k)
		}
	}
	if err := ValidateObjectKey(strings.Repeat("a", 1025)); err == nil {
		t.Error("overly long key should be invalid")
	}
}

func TestValidateBucketNameOK(t *testing.T) {
	for _, n := range []string{"abc", "my-bucket", "bucket.name", "a.b.c"} {
		if err := ValidateBucketName(n); err != nil {
			t.Errorf("bucket %q should be valid: %v", n, err)
		}
	}
}

func TestValidateBucketNameReject(t *testing.T) {
	bad := []string{"", "ab", "MyBucket", "bucket/", "bucket..name", "-bucket", "bucket-"}
	for _, n := range bad {
		if err := ValidateBucketName(n); err == nil {
			t.Errorf("bucket %q should be invalid", n)
		}
	}
}

func TestValidateCIDOK(t *testing.T) {
	for _, c := range []string{
		"bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		"bae...something",
		"z...something",
		"b...",
	} {
		if err := ValidateCID(c); err != nil {
			t.Errorf("CID %q should be valid: %v", c, err)
		}
	}
}

func TestValidateCIDReject(t *testing.T) {
	bad := []string{"", "ab", "notacid", "http://example.com", "/ipfs/bafy"}
	for _, c := range bad {
		if err := ValidateCID(c); err == nil {
			t.Errorf("CID %q should be invalid", c)
		}
	}
}
