// Package server serves the static database over HTTP.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/jgf"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
	"github.com/converged-computing/google-cloud-topology/pkg/sizing"
)

// Server holds the loaded database and serves queries against it.
type Server struct {
	dir       string
	quotaPath string
	mu        sync.RWMutex
	db        *database.Database
	snap      *quota.Snapshot
	loadedAt  time.Time
}

// New loads the database at dir and, when quotaPath is set, a quota snapshot used to
// size graphs and answer capacity queries.
func New(dir, quotaPath string) (*Server, error) {
	s := &Server{dir: dir, quotaPath: quotaPath}
	return s, s.Reload()
}

// Reload re-reads the database and quota snapshot from disk.
func (s *Server) Reload() error {
	db, err := database.Load(s.dir)
	if err != nil {
		return err
	}
	var snap *quota.Snapshot
	if s.quotaPath != "" {
		if snap, err = quota.Load(s.quotaPath); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.db, s.snap, s.loadedAt = db, snap, time.Now()
	s.mu.Unlock()
	return nil
}

func (s *Server) current() (*database.Database, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db, s.loadedAt
}

func (s *Server) quota() *quota.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /v1/index", s.index)
	mux.HandleFunc("GET /v1/machine-types", s.listMachineTypes)
	mux.HandleFunc("GET /v1/machine-types/{name}", s.getMachineType)
	mux.HandleFunc("GET /v1/machine-types/{name}/topology", s.getTopology)
	mux.HandleFunc("GET /v1/machine-types/{name}/capture", s.getCapture)
	mux.HandleFunc("GET /v1/graph", s.graph)
	mux.HandleFunc("GET /v1/capacity", s.capacity)
	mux.HandleFunc("GET /v1/sizes", s.sizes)
	return mux
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	db, at := s.current()
	writeJSON(w, http.StatusOK, db.BuildIndex(at))
}

func (s *Server) listMachineTypes(w http.ResponseWriter, r *http.Request) {
	db, _ := s.current()
	q := r.URL.Query()
	minGPUs, err := intParam(q.Get("min_gpus"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "min_gpus must be an integer")
		return
	}
	list := db.Query(database.Filter{
		Region:      q.Get("region"),
		Zone:        q.Get("zone"),
		Family:      q.Get("family"),
		Accelerator: q.Get("accelerator"),
		MinGPUs:     minGPUs,
	})
	if list == nil {
		list = []database.MachineType{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getMachineType(w http.ResponseWriter, r *http.Request) {
	db, _ := s.current()
	mt, ok := db.MachineTypes[r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "machine type not found")
		return
	}
	writeJSON(w, http.StatusOK, mt)
}

func (s *Server) getTopology(w http.ResponseWriter, r *http.Request) {
	db, _ := s.current()
	name := r.PathValue("name")
	path := ""
	if _, ok := db.Captures[name]; ok {
		path = db.HwlocPath(name)
	}
	if path == "" {
		writeError(w, http.StatusNotFound, "no topology captured for this machine type")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Write(data)
}

func (s *Server) getCapture(w http.ResponseWriter, r *http.Request) {
	db, _ := s.current()
	c, ok := db.Captures[r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "no topology captured for this machine type")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	db, _ := s.current()
	q := r.URL.Query()
	nodes, err := intParam(q.Get("nodes"), 1)
	if err != nil || nodes < 1 || nodes > 1024 {
		writeError(w, http.StatusBadRequest, "nodes must be an integer from 1 to 1024")
		return
	}
	opts := jgf.Options{
		Region:       q.Get("region"),
		MachineTypes: splitParam(q.Get("machine_types")),
		Models:       splitParam(q.Get("models")),
		Nodes:        nodes,
	}
	if snap := s.quota(); snap != nil {
		opts.Capacity = snap.NodesFunc(opts.Region, nil)
		if q.Get("nodes") != "" {
			opts.MaxNodes = nodes
		}
	}
	var g *jgf.Graph
	switch level := q.Get("level"); level {
	case "series":
		sopts := jgf.SeriesOptions{Region: opts.Region, Series: splitParam(q.Get("series")), Models: opts.Models, Nodes: nodes}
		if snap := s.quota(); snap != nil {
			sopts.Units = snap.UnitsFunc(opts.Region, nil)
		}
		g, err = jgf.BuildSeries(db, sopts)
	case "", "coarse":
		g, err = jgf.BuildCoarse(db, opts)
	case "exact":
		g, err = jgf.BuildExact(db, opts, jgf.HwlocLoader(db))
	default:
		err = errors.New("level must be series, coarse or exact")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) capacity(w http.ResponseWriter, r *http.Request) {
	snap := s.quota()
	if snap == nil {
		writeError(w, http.StatusNotFound, "server started without a quota snapshot")
		return
	}
	db, _ := s.current()
	region := r.URL.Query().Get("region")
	if region == "" {
		writeError(w, http.StatusBadRequest, "region is required")
		return
	}
	models := splitParam(r.URL.Query().Get("models"))
	if len(models) == 0 {
		models = []string{"on-demand"}
	}
	out := []quota.Capacity{}
	for _, mt := range db.Query(database.Filter{Region: region}) {
		for _, model := range models {
			out = append(out, quota.Compute(snap, mt, model, region))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// sizes lists machine types and counts in a series that satisfy a request for total
// vcpus, gpus and memory_gb, within quota when the server has a snapshot.
func (s *Server) sizes(w http.ResponseWriter, r *http.Request) {
	db, _ := s.current()
	q := r.URL.Query()
	region, series := q.Get("region"), q.Get("series")
	if region == "" || series == "" {
		writeError(w, http.StatusBadRequest, "region and series are required")
		return
	}
	var req sizing.Request
	var err error
	for _, p := range []struct {
		name string
		dst  *int
	}{{"vcpus", &req.VCPUs}, {"gpus", &req.GPUs}, {"memory_gb", &req.MemoryGB}, {"max_nodes", &req.MaxNodes}} {
		if *p.dst, err = intParam(q.Get(p.name), 0); err != nil || *p.dst < 0 {
			writeError(w, http.StatusBadRequest, p.name+" must be a non-negative integer")
			return
		}
	}
	members := database.Series(db.Query(database.Filter{Region: region}))[series]
	if len(members) == 0 {
		writeError(w, http.StatusNotFound, "no machine types in that series and region")
		return
	}
	model := q.Get("model")
	if model == "" {
		model = "on-demand"
	}
	lim := sizing.Limits{VCPUs: -1, GPUs: -1}
	if snap := s.quota(); snap != nil {
		u := quota.SeriesUnits(snap, series, members, model, region)
		lim = sizing.Limits{VCPUs: u.VCPUs, GPUs: u.GPUs}
	}
	opts := sizing.Options(members, req, lim)
	if opts == nil {
		opts = []sizing.Option{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series, "region": region, "model": model, "limits": lim, "options": opts})
}

func intParam(v string, def int) (int, error) {
	if v == "" {
		return def, nil
	}
	return strconv.Atoi(v)
}

func splitParam(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
