package ipfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// NodeClient talks to a single IPFS node's HTTP API.
type NodeClient struct {
	Endpoint string
	HTTP     *http.Client
}

func NewNodeClient(endpoint string) *NodeClient {
	return &NodeClient{
		Endpoint: strings.TrimSuffix(endpoint, "/"),
		HTTP:     &http.Client{Timeout: 0}, // no deadline; callers pass a context
	}
}

func (c *NodeClient) apiPath(method, path string, params url.Values, body io.Reader, contentType string) (*http.Response, error) {
	u := c.Endpoint + path
	if params != nil {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.HTTP.Do(req)
}

// AddOptions controls how content is added to the node. Identical options on
// identical bytes produce the same CID, which is required for content
// deduplication across S3 objects.
type AddOptions struct {
	Pin       bool   // pin the added content recursively
	CIDVersion int    // CID version (0 or 1)
	RawLeaves  bool   // use raw leaves
	Chunker    string // chunking algorithm, e.g. "size-1048576"
	Quieter    bool   // suppress progress output
}

// Add streams src into the node as a multipart form (field "file") and returns
// the CID and size of the added object. The whole body is streamed through an
// io.Pipe; the caller's context cancels the upload by closing the pipe. The
// content is pinned when Pin is true.
func (c *NodeClient) Add(ctx context.Context, src io.Reader, name string, opts AddOptions) (string, int64, error) {
	pr, pw := io.Pipe()
	mp := multipart.NewWriter(pw)

	errc := make(chan error, 1)
	go func() {
		defer pw.Close()
		// Kubo's /api/v0/add expects a multipart form field named "file"
		// whose value is the content. Stream src into it without buffering.
		w, err := mp.CreateFormFile("file", name)
		if err != nil {
			errc <- err
			return
		}
		if _, err := io.Copy(w, src); err != nil {
			errc <- err
			return
		}
		if err := mp.Close(); err != nil {
			errc <- err
		}
	}()

	params := url.Values{}
	if opts.Pin {
		params.Set("pin", "true")
	}
	if opts.CIDVersion > 0 {
		params.Set("cid-version", fmt.Sprint(opts.CIDVersion))
	}
	if opts.RawLeaves {
		params.Set("raw-leaves", "true")
	}
	if opts.Chunker != "" {
		params.Set("chunker", opts.Chunker)
	}
	if opts.Quieter {
		params.Set("quieter", "true")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint+"/api/v0/add?"+params.Encode(), pr)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Context cancellation or pipe closure.
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("add failed: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, err
	}
	// Kubo streams NDJSON; the final line is the result for the whole file.
	var result struct {
		CID  string `json:"Hash"`
		Size int64  `json:"Size"`
	}
	if err := unmarshalLastLine(body, &result); err != nil {
		return "", 0, err
	}
	if result.CID == "" {
		return "", 0, fmt.Errorf("add returned empty CID")
	}
	return result.CID, result.Size, nil
}

// unmarshalLastLine parses the last non-empty JSON line of an NDJSON stream.
func unmarshalLastLine(b []byte, v interface{}) error {
	var last []byte
	for len(b) > 0 {
		var line []byte
		line, b = splitLine(b)
		if len(strings.TrimSpace(string(line))) > 0 {
			last = line
		}
	}
	if len(last) == 0 {
		return fmt.Errorf("empty add response")
	}
	return json.Unmarshal(last, v)
}

func splitLine(b []byte) (line, rest []byte) {
	i := strings.IndexByte(string(b), '\n')
	if i < 0 {
		return b, nil
	}
	return b[:i], b[i+1:]
}

// Cat streams the content of a CID from the node. offset and length, when
// non-zero, request a byte range (Kubo supports offset/length on cat). The
// caller must close the returned reader.
func (c *NodeClient) Cat(ctx context.Context, cid string, offset, length int64) (io.ReadCloser, error) {
	params := url.Values{"arg": {cid}}
	if offset > 0 {
		params.Set("offset", fmt.Sprint(offset))
	}
	if length > 0 {
		params.Set("length", fmt.Sprint(length))
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.Endpoint+"/api/v0/cat?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("cat failed: %s", resp.Status)
	}
	return resp.Body, nil
}

// ID returns the node's peer ID and swarm addresses.
func (c *NodeClient) ID(ctx context.Context) (string, []string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint+"/api/v0/id", nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("id failed: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	var result struct {
		ID string   `json:"ID"`
		Addresses []string `json:"Addresses"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", nil, err
	}
	return result.ID, result.Addresses, nil
}

// PinAdd tells the node to pin a CID recursively.
func (c *NodeClient) PinAdd(cid string) error {
	params := url.Values{"arg": {cid}}
	resp, err := c.apiPath("POST", "/api/v0/pin/add", params, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pin add failed: %s", resp.Status)
	}
	_, _ = io.ReadAll(resp.Body)
	return nil
}

// PinRemove tells the node to unpin a CID recursively.
func (c *NodeClient) PinRemove(cid string) error {
	params := url.Values{"arg": {cid}, "recursive": {"true"}}
	resp, err := c.apiPath("POST", "/api/v0/pin/rm", params, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pin rm failed: %s", resp.Status)
	}
	_, _ = io.ReadAll(resp.Body)
	return nil
}

var unmarshal = func(b []byte, v interface{}) error {
	return json.Unmarshal(b, v)
}
