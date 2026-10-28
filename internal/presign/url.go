package presign

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func sign(accessKey, secret, method, region string, expires time.Duration, resource string, query url.Values) (string, string) {
	now := time.Now().UTC()
	date := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	credential := accessKey + "/" + date + "/" + region + "/s3/aws4_request"

	q := query.Encode()
	if q != "" {
		q = "&" + q
	}
	qs := fmt.Sprintf("X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=%s&X-Amz-Date=%s&X-Amz-Expires=%d&X-Amz-SignedHeaders=host%s",
		url.QueryEscape(credential), amzDate, int(expires.Seconds()), q)

	canonicalURI := resource
	if strings.Contains(canonicalURI, "?") {
		canonicalURI = strings.SplitN(canonicalURI, "?", 2)[0]
	}

	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		qs,
		"host:" + hostFromResource(resource),
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")

	hashedCanonical := fmt.Sprintf("%x", sha256.Sum256([]byte(canonicalRequest)))

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		date + "/" + region + "/s3/aws4_request",
		hashedCanonical,
	}, "\n")

	sigKey := signingKey(secret, date, region)
	signature := hexHMAC(sigKey, []byte(stringToSign))

	signedURL := resource
	if strings.Contains(signedURL, "?") {
		signedURL += "&"
	} else {
		signedURL += "?"
	}
	signedURL += qs + "&X-Amz-Signature=" + signature

	return date, signedURL
}

func hostFromResource(resource string) string {
	u, err := url.Parse(resource)
	if err != nil {
		return ""
	}
	return u.Host
}

func signingKey(secret, date, region string) []byte {
	kDate := hmacSha256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSha256(kDate, []byte(region))
	kService := hmacSha256(kRegion, []byte("s3"))
	return hmacSha256(kService, []byte("aws4_request"))
}

func hmacSha256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hexHMAC(key, data []byte) string {
	return fmt.Sprintf("%x", hmacSha256(key, data))
}

// GetObject returns a pre-signed GET URL for an object.
func GetObject(accessKey, secret, region, bucket, key string, expires time.Duration) string {
	resource := fmt.Sprintf("/%s/%s", bucket, key)
	query := url.Values{}
	_, url := sign(accessKey, secret, "GET", region, expires, resource, query)
	return url
}

// PutObject returns a pre-signed PUT URL for an object.
func PutObject(accessKey, secret, region, bucket, key string, expires time.Duration) string {
	resource := fmt.Sprintf("/%s/%s", bucket, key)
	query := url.Values{}
	_, url := sign(accessKey, secret, "PUT", region, expires, resource, query)
	return url
}
