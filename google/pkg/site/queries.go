package site

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed queries
var queryFiles embed.FS

// Queries are the Cypher queries shipped with cmd/graphdb, by name, for the Motifs page.
var Queries = func() map[string]string {
	out := map[string]string{}
	entries, _ := fs.ReadDir(queryFiles, "queries")
	for _, e := range entries {
		data, err := fs.ReadFile(queryFiles, "queries/"+e.Name())
		if err == nil {
			out[strings.TrimSuffix(e.Name(), ".cypher")] = string(data)
		}
	}
	return out
}()

// FoldNote explains what the site's graph views show.
const FoldNote = "The graphs are drawn and counted from the fold: siblings with the same shape are one entry with a count, so a node's 96 cores are one core entry ×96. The vertex and edge totals on the Resource graphs page are expanded counts, every core counted; the tree and these motifs show the folded structure."
