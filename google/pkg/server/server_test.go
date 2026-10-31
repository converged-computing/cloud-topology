package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/converged-computing/google-cloud-topology/pkg/client"
	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

func start(t *testing.T) *client.Client {
	t.Helper()
	srv, err := New("../../testdata", "")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return client.New(ts.URL)
}

func TestMachineTypes(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	all, err := c.MachineTypes(ctx, database.Filter{Region: "us-central1"})
	if err != nil || len(all) != 2 {
		t.Fatalf("region query: %v, %d results", err, len(all))
	}
	gpu, err := c.MachineTypes(ctx, database.Filter{MinGPUs: 2})
	if err != nil || len(gpu) != 1 || gpu[0].Name != "test-gpu-2g" {
		t.Fatalf("min_gpus query: %v %+v", err, gpu)
	}
	if _, err := c.MachineType(ctx, "does-not-exist"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("want 404 error, got %v", err)
	}
}

func TestTopologyAndIndex(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	xml, err := c.Topology(ctx, "test-gpu-2g")
	if err != nil || !strings.Contains(string(xml), "<topology") {
		t.Fatalf("topology: %v", err)
	}
	for _, name := range []string{"test-cpu-4", "..", "%2e%2e"} {
		if _, err := c.Topology(ctx, name); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	idx, err := c.Index(ctx)
	if err != nil || len(idx.MachineTypes) != 2 || len(idx.Topologies) != 1 {
		t.Fatalf("index: %v %+v", err, idx)
	}
}

func TestGraph(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	data, err := c.Graph(ctx, client.GraphRequest{Level: "exact", Region: "us-central1", MachineTypes: []string{"test-gpu-2g"}, Nodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Graph struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || len(doc.Graph.Nodes) == 0 {
		t.Fatalf("graph json: %v", err)
	}
	if _, err := c.Graph(ctx, client.GraphRequest{Level: "bogus", Region: "us-central1"}); err == nil {
		t.Error("bad level should fail")
	}
}

func TestBadParams(t *testing.T) {
	srv, err := New("../../testdata", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"/v1/machine-types?min_gpus=x", "/v1/graph?region=us-central1&nodes=0"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", u, rec.Code)
		}
	}
}

func TestCapacityWithQuota(t *testing.T) {
	srv, err := New("../../testdata/quota-example", "../../testdata/quota-example/quota.json")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/capacity?region=us-central1", nil))
	var caps []struct {
		MachineType string `json:"machine_type"`
		Nodes       int    `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("capacity: %d %v", rec.Code, err)
	}
	got := map[string]int{}
	for _, c := range caps {
		got[c.MachineType] = c.Nodes
	}
	if got["n2-standard-4"] != 5 || got["g2-standard-8"] != 2 || got["a3-ultragpu-8g"] != 0 {
		t.Errorf("unexpected capacity %v", got)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/graph?region=us-central1", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "a3-ultragpu-8g") {
		t.Errorf("graph sized by quota should omit offers without capacity (status %d)", rec.Code)
	}
	// n2-standard-2/4/8: 10, 5, 2 nodes from 20 N2 vCPUs; g2-standard-8/24: 2 and 1 from 2 L4 GPUs.
	if n := strings.Count(rec.Body.String(), `"type": "node"`); n != 20 {
		t.Errorf("want 20 quota-sized nodes without a nodes parameter, got %d", n)
	}
}

func TestCapacityWithoutQuota(t *testing.T) {
	srv, err := New("../../testdata", "")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/capacity?region=us-central1", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404 without a quota snapshot, got %d", rec.Code)
	}
}

func TestSizesAndSeriesGraph(t *testing.T) {
	srv, err := New("../../testdata/quota-example", "../../testdata/quota-example/quota.json")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sizes?region=us-central1&series=n2-standard&vcpus=16", nil))
	var resp struct {
		Options []struct {
			MachineType string `json:"machine_type"`
			Count       int    `json:"count"`
		} `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("sizes: %d %v", rec.Code, err)
	}
	if len(resp.Options) != 3 || resp.Options[0].MachineType != "n2-standard-8" || resp.Options[0].Count != 2 {
		t.Errorf("unexpected sizes %+v", resp.Options)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/graph?level=series&region=us-central1", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "n2-standard.on-demand") || strings.Contains(body, "a3-ultragpu") {
		t.Errorf("series graph: status %d", rec.Code)
	}
	// 20 N2 vCPUs plus 24 g2 vCPUs (2 GPUs x 12 vCPUs per GPU).
	if n := strings.Count(body, `"type": "core"`); n != 44 {
		t.Errorf("want 44 core vertices, got %d", n)
	}
}
