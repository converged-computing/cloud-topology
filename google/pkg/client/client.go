// Package client queries the metadata server.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

// Client talks to a metadata server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for the server at baseURL, e.g. http://localhost:8080.
func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: http.DefaultClient}
}

// GraphRequest selects a graph to build.
type GraphRequest struct {
	Level        string
	Region       string
	MachineTypes []string
	Models       []string
	Nodes        int
}

// Index returns the database summary.
func (c *Client) Index(ctx context.Context) (database.Index, error) {
	var idx database.Index
	return idx, c.getJSON(ctx, "/v1/index", nil, &idx)
}

// MachineTypes lists machine types matching the filter.
func (c *Client) MachineTypes(ctx context.Context, f database.Filter) ([]database.MachineType, error) {
	q := url.Values{}
	set(q, "region", f.Region)
	set(q, "zone", f.Zone)
	set(q, "family", f.Family)
	set(q, "accelerator", f.Accelerator)
	if f.MinGPUs > 0 {
		q.Set("min_gpus", strconv.Itoa(f.MinGPUs))
	}
	var out []database.MachineType
	return out, c.getJSON(ctx, "/v1/machine-types", q, &out)
}

// MachineType returns one machine type.
func (c *Client) MachineType(ctx context.Context, name string) (database.MachineType, error) {
	var mt database.MachineType
	return mt, c.getJSON(ctx, "/v1/machine-types/"+url.PathEscape(name), nil, &mt)
}

// Topology returns the captured hwloc XML for a machine type.
func (c *Client) Topology(ctx context.Context, name string) ([]byte, error) {
	return c.get(ctx, "/v1/machine-types/"+url.PathEscape(name)+"/topology", nil)
}

// Graph returns a JGF document.
func (c *Client) Graph(ctx context.Context, req GraphRequest) (json.RawMessage, error) {
	q := url.Values{}
	set(q, "level", req.Level)
	set(q, "region", req.Region)
	set(q, "machine_types", strings.Join(req.MachineTypes, ","))
	set(q, "models", strings.Join(req.Models, ","))
	if req.Nodes > 0 {
		q.Set("nodes", strconv.Itoa(req.Nodes))
	}
	data, err := c.get(ctx, "/v1/graph", q)
	return json.RawMessage(data), err
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, v any) error {
	data, err := c.get(ctx, path, q)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s: %s", resp.Status, e.Error)
		}
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return data, nil
}

func set(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}
