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
