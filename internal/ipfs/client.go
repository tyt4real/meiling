package ipfs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NodeClient talks to a single IPFS node's HTTP API.
type NodeClient struct {
	Endpoint string
	HTTP     *http.Client
}

func NewNodeClient(endpoint string) *NodeClient {
	return &NodeClient{
		Endpoint: strings.TrimSuffix(endpoint, "/"),
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *NodeClient) apiPath(method, path string, params url.Values, body io.Reader) (*http.Response, error) {
	u := c.Endpoint + path
	if params != nil {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	return c.HTTP.Do(req)
}

// PinAdd tells the node to pin a CID recursively.
func (c *NodeClient) PinAdd(cid string) error {
	params := url.Values{"arg": {cid}}
	resp, err := c.apiPath("POST", "/api/v0/pin/add", params, nil)
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

// Cat streams the content of a CID from the node. The caller must close the
// returned reader. The full file is streamed; the IPFS HTTP API does not
// expose byte-range reads for cat, so callers seeking efficient range access
// should cache the content locally (see store.IPFS).
func (c *NodeClient) Cat(cid string) (io.ReadCloser, error) {
	params := url.Values{"arg": {cid}}
	resp, err := c.apiPath("GET", "/api/v0/cat", params, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("cat failed: %s", resp.Status)
	}
	return resp.Body, nil
}

// Add streams src into the node and returns the CID and size of the added
// object. The content is not pinned; call PinAdd separately to retain it.
func (c *NodeClient) Add(src io.Reader) (string, int64, error) {
	params := url.Values{"arg": {}}
	resp, err := c.apiPath("POST", "/api/v0/add", params, src)
	if err != nil {
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
	var result struct {
		CID  string `json:"Hash"`
		Size int64  `json:"Size"`
	}
	if err := unmarshal(body, &result); err != nil {
		return "", 0, err
	}
	return result.CID, result.Size, nil
}

var unmarshal = func(b []byte, v interface{}) error {
	return json.Unmarshal(b, v)
}

// PinRemove tells the node to unpin a CID recursively.
func (c *NodeClient) PinRemove(cid string) error {
	params := url.Values{"arg": {cid}, "recursive": {"true"}}
	resp, err := c.apiPath("POST", "/api/v0/pin/rm", params, nil)
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

// PinLS checks whether a CID is pinned on this node.
func (c *NodeClient) PinLS(cid string) (bool, error) {
	params := url.Values{"arg": {cid}, "type": {"recursive"}}
	resp, err := c.apiPath("POST", "/api/v0/pin/ls", params, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, nil
	}
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Keys map[string]struct {
			Type string `json:"Type"`
		} `json:"Keys"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, err
	}
	for _, v := range result.Keys {
		if v.Type == "recursive" {
			return true, nil
		}
	}
	return false, nil
}

// ID returns the node's peer ID.
func (c *NodeClient) ID() (string, error) {
	resp, err := c.apiPath("POST", "/api/v0/id", nil, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	return result.ID, nil
}

var _ = fmt.Sprintf
