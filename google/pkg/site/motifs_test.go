package site

import (
	"reflect"
	"testing"
)

// One merged graph with two clouds: a socket shape both share is one motif seen in both,
// each attributed to its own database and region.
func TestMotifsAcrossProvidersInOneMergedGraph(t *testing.T) {
	socket := func() *Tree { return &Tree{Type: "socket", Count: 2, Vertices: 2, Signature: "sock"} }
	offer := func(mt, sig string) *Tree {
		return &Tree{Type: "offer", Count: 1, Vertices: 4, Signature: sig, Properties: map[string]string{"machine_type": mt},
			Children: []*Tree{{Type: "node", Count: 1, Vertices: 3, Signature: "node-" + sig, Children: []*Tree{socket()}}}}
	}
	cloud := func(provider, region, zone, mt string) *Tree {
		return &Tree{Type: "provider", Name: provider, Count: 1, Children: []*Tree{{Type: "region", Name: region, Count: 1,
			Children: []*Tree{{Type: "zone", Name: zone, Count: 1, Children: []*Tree{offer(mt, "offer-"+mt)}}}}}}
	}
	root := &Tree{Type: "site", Count: 1, Children: []*Tree{cloud("google-cloud", "us-central1", "us-central1-a", "n2-standard-8"), cloud("aws", "us-east-1", "us-east-1a", "m6i.2xlarge")}}
	infos := []GraphInfo{
		{Database: "compute-engine", Provider: "google-cloud", Level: "exact", Region: "us-central1", File: "data/graphs/exact.json"},
		{Database: "aws", Provider: "aws", Level: "exact", Region: "us-east-1", File: "data/graphs/exact.json"},
	}
	var sock *Motif
	for _, m := range Motifs(map[string]*Tree{"data/graphs/exact.json": root}, infos) {
		if m.Signature == "sock" {
			sock = &m
		}
	}
	if sock == nil {
		t.Fatal("no socket motif")
	}
	if !reflect.DeepEqual(sock.Graphs, []string{"aws", "compute-engine"}) || !reflect.DeepEqual(sock.Regions, []string{"us-central1", "us-east-1"}) {
		t.Errorf("graphs %v regions %v", sock.Graphs, sock.Regions)
	}
	if sock.Occurrences != 4 || !reflect.DeepEqual(sock.MachineTypes, []string{"m6i.2xlarge", "n2-standard-8"}) {
		t.Errorf("occurrences %d machine types %v", sock.Occurrences, sock.MachineTypes)
	}
}
