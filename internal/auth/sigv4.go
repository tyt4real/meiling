package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	Algorithm = "AWS4-HMAC-SHA256"
	Service   = "s3"
	Request   = "aws4_request"
)

// Credential parses the Credential= element of an Authorization header.
type Credential struct {
	AccessKeyID string
	Date        string
	Region      string
	Service     string
	Request     string
}

// AuthInfo holds everything extracted from an incoming signed request.
type AuthInfo struct {
	Credential   Credential
	SignedHeaders []string
	Signature    string
	Headers      map[string]string // lowercased header name -> value
	Method       string
	URI          string
	Query        string
	BodyHash     string
	RequestTime  time.Time
}

func hexHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ParseAuth extracts SigV4 auth info from an HTTP request.
func ParseAuth(r *http.Request) (*AuthInfo, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return nil, fmt.Errorf("missing Authorization header")
	}
	if !strings.HasPrefix(auth, Algorithm+" ") {
		return nil, fmt.Errorf("unsupported authorization scheme")
	}
	payload := strings.TrimPrefix(auth, Algorithm+" ")

	parts := map[string]string{}
	// Split on commas, but the first chunk contains "Algorithm Credential=...".
	chunks := strings.Split(payload, ",")
	for i, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if i == 0 {
			// First chunk: "AWS4-HMAC-SHA256 Credential=AKIA/..."
			idx := strings.Index(chunk, "Credential=")
			if idx < 0 {
				return nil, fmt.Errorf("missing Credential")
			}
			credPart := chunk[idx+len("Credential="):]
			parts["Credential"] = credPart
			continue
		}
		kv := strings.SplitN(chunk, "=", 2)
		if len(kv) != 2 {
			continue
		}
		parts[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}

	credRaw, ok := parts["Credential"]
	if !ok {
		return nil, fmt.Errorf("missing Credential")
	}
	credParts := strings.Split(credRaw, "/")
	if len(credParts) != 5 {
		return nil, fmt.Errorf("malformed Credential")
	}
	cred := Credential{
		AccessKeyID: credParts[0],
		Date:        credParts[1],
		Region:      credParts[2],
		Service:     credParts[3],
		Request:     credParts[4],
	}

	signedHeadersRaw, ok := parts["SignedHeaders"]
	if !ok {
		return nil, fmt.Errorf("missing SignedHeaders")
	}
	signedHeaders := strings.Split(signedHeadersRaw, ";")

	signature, ok := parts["Signature"]
	if !ok {
		return nil, fmt.Errorf("missing Signature")
	}

	// Build lowercased header map.
	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}
	// Go promotes the Host header to r.Host and removes it from r.Header.
	if r.Host != "" && headers["host"] == "" {
		headers["host"] = r.Host
	}

	// Canonical query string (sorted).
	query := r.URL.RawQuery

	// Body hash: use x-amz-content-sha256 header if present, else hash body.
	bodyHash := headers["x-amz-content-sha256"]
	if bodyHash == "" {
		// We cannot easily re-read the body here; caller should set it.
		bodyHash = headers["x-amz-content-sha256"]
	}
	if bodyHash == "" {
		bodyHash = hexHash([]byte{}) // empty body placeholder
	}

	// Parse request time from x-amz-date or Date header.
	reqTime := time.Now()
	if t, ok := headers["x-amz-date"]; ok {
		if parsed, err := time.Parse("20060102T150405Z", t); err == nil {
			reqTime = parsed
		}
	}

	return &AuthInfo{
		Credential:    cred,
		SignedHeaders: signedHeaders,
		Signature:     signature,
		Headers:       headers,
		Method:        r.Method,
		URI:           r.URL.Path,
		Query:         query,
		BodyHash:      bodyHash,
		RequestTime:   reqTime,
	}, nil
}

// CanonicalRequest builds the SigV4 canonical request string.
func (a *AuthInfo) CanonicalRequest() string {
	// Canonical headers: only the signed headers, lowercased, sorted,
	// "key:value\n", trimmed whitespace on value.
	signedSet := make(map[string]bool)
	for _, h := range a.SignedHeaders {
		signedSet[strings.ToLower(h)] = true
	}
	var headerKeys []string
	for k := range signedSet {
		headerKeys = append(headerKeys, k)
	}
	sort.Strings(headerKeys)

	var cb strings.Builder
	for _, k := range headerKeys {
		cb.WriteString(k)
		cb.WriteString(":")
		cb.WriteString(strings.TrimSpace(a.Headers[k]))
		cb.WriteString("\n")
	}

	signed := strings.Join(a.SignedHeaders, ";")

	return strings.Join([]string{
		a.Method,
		a.URI,
		a.Query,
		cb.String(),
		signed,
		a.BodyHash,
	}, "\n")
}

// StringToSign builds the string to sign.
func (a *AuthInfo) StringToSign(canonicalHash string) string {
	return strings.Join([]string{
		Algorithm,
		a.RequestTime.UTC().Format("20060102T150405Z"),
		a.Credential.Date + "/" + a.Credential.Region + "/" + a.Credential.Service + "/" + a.Credential.Request,
		canonicalHash,
	}, "\n")
}

// SigningKey derives the AWS signing key from the secret.
func SigningKey(secret, date, region, service string) []byte {
	kDate := hmacSha256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSha256(kDate, []byte(region))
	kService := hmacSha256(kRegion, []byte(service))
	return hmacSha256(kService, []byte(Request))
}

// Signature computes the hex signature.
func Signature(key []byte, stringToSign string) string {
	return hex.EncodeToString(hmacSha256(key, []byte(stringToSign)))
}

// Validate checks the request signature against the provided secret.
func Validate(a *AuthInfo, secret string) bool {
	key := SigningKey(secret, a.Credential.Date, a.Credential.Region, a.Credential.Service)
	canonicalHash := hexHash([]byte(a.CanonicalRequest()))
	sts := a.StringToSign(canonicalHash)
	expected := Signature(key, sts)
	return hmac.Equal([]byte(expected), []byte(a.Signature))
}

func hmacSha256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// Middleware returns an http.Handler that validates SigV4 and sets the access key on the context.
type Middleware struct {
	Next       http.Handler
	GetSecret  func(accessKeyID string) (string, error) // returns secret or ErrNotFound
	OnUnauth   http.Handler
}

func (m *Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	info, err := ParseAuth(r)
	if err != nil {
		if m.OnUnauth != nil {
			m.OnUnauth.ServeHTTP(w, r)
		} else {
			http.Error(w, "missing auth", http.StatusUnauthorized)
		}
		return
	}
	secret, err := m.GetSecret(info.Credential.AccessKeyID)
	if err != nil {
		if m.OnUnauth != nil {
			m.OnUnauth.ServeHTTP(w, r)
		} else {
			http.Error(w, "invalid access key", http.StatusUnauthorized)
		}
		return
	}
	if !Validate(info, secret) {
		key := SigningKey(secret, info.Credential.Date, info.Credential.Region, info.Credential.Service)
		canonicalHash := hexHash([]byte(info.CanonicalRequest()))
		sts := info.StringToSign(canonicalHash)
		expected := Signature(key, sts)
		slog.Warn("sigv4 validation failed", "access_key", info.Credential.AccessKeyID, "expected", expected, "actual", info.Signature, "sts", sts, "canonical", info.CanonicalRequest())
		if m.OnUnauth != nil {
			m.OnUnauth.ServeHTTP(w, r)
		} else {
			http.Error(w, "signature mismatch", http.StatusUnauthorized)
		}
		return
	}
	r = r.WithContext(contextWithAccessKey(r.Context(), info.Credential.AccessKeyID))
	m.Next.ServeHTTP(w, r)
}
