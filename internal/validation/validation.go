// Package validation provides request validation utilities.
package validation

import (
	"fmt"
	"strings"
)

// ValidateObjectKey rejects keys that could escape the bucket storage root.
func ValidateObjectKey(key string) error {
	if key == "" {
		return fmt.Errorf("object key must not be empty")
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("object key must not start with '/'")
	}
	if strings.Contains(key, "..") {
		return fmt.Errorf("object key must not contain '..'")
	}
	// Reject null bytes.
	if strings.Contains(key, "\x00") {
		return fmt.Errorf("object key must not contain null bytes")
	}
	// Reject overly long keys (S3 limit is 1024 bytes).
	if len(key) > 1024 {
		return fmt.Errorf("object key exceeds 1024 bytes")
	}
	return nil
}

// ValidateBucketName performs basic S3 bucket name validation.
func ValidateBucketName(name string) error {
	if name == "" {
		return fmt.Errorf("bucket name must not be empty")
	}
	if len(name) < 3 || len(name) > 63 {
		return fmt.Errorf("bucket name must be between 3 and 63 characters")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("bucket name must not contain '..'")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("bucket name must not contain '/'")
	}
	if !isLowercaseDNSSafe(name) {
		return fmt.Errorf("bucket name must be lowercase DNS-compliant")
	}
	return nil
}

func isLowercaseDNSSafe(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] == '-' || s[0] == '.' || s[len(s)-1] == '-' || s[len(s)-1] == '.' {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// ValidateCID checks that a CID string has a valid multibase/multicodec prefix.
// Accepts common Filebase/IPFS CID prefixes (base58btc: bafy..., base32: bae...).
func ValidateCID(cid string) error {
	if cid == "" {
		return fmt.Errorf("CID must not be empty")
	}
	if len(cid) < 2 {
		return fmt.Errorf("CID too short")
	}
	prefix := cid[:2]
	valid := []string{
		"bafy", "bae", "bag", "bah", "baj", "bak", "bal", "bam", "ban", "bao",
		"bap", "baq", "bar", "bas", "bat", "bau", "bav", "baw", "bax", "bay",
		"baz", "bbb", "bbc", "bbd", "bbe", "bbf", "bbg", "bbh", "bbi", "bbj",
		"bbk", "bbl", "bbm", "bbn", "bbo", "bbp", "bbq", "bbr", "bbs", "bbt",
		"bbu", "bbv", "bbw", "bbx", "bby", "bbz",
	}
	for _, v := range valid {
		if strings.HasPrefix(cid, v) {
			return nil
		}
	}
	// Allow raw base32 (b...) and base58 (z...) single-char prefixes too.
	if len(prefix) >= 1 {
		first := cid[0]
		if first == 'b' || first == 'z' || first == 'B' || first == 'Z' {
			return nil
		}
	}
	return fmt.Errorf("CID does not start with a recognized multibase prefix")
}
