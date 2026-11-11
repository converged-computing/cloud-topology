package graphdb

import "fmt"

// Schema is the LadybugDB (Cypher) schema the tables load into: Vertex and Shape node
// tables, a Contains relationship carrying the folded count, HasShape, and Allocation,
// the scheduler's record of nodes of an offer taken for a span of time. An allocation
// names its offer by path (stable across rebuilds), so load carries the table over when
// it recreates the database. Free nodes of an offer at a time are its quota count less
// the allocations covering that time; see cmd/graphdb's README and ingest-agent.
const Schema = `CREATE NODE TABLE IF NOT EXISTS Vertex(
  id INT64 PRIMARY KEY, provider STRING, graph STRING, level STRING, region STRING, zone STRING, offer STRING,
  type STRING, name STRING, path STRING, depth INT32, count INT64, vertices INT64, size INT64, unit STRING,
  exclusive BOOLEAN, signature STRING, parent_id INT64, properties STRING,
  machine_type STRING, family STRING, provisioning_model STRING, vcpus INT64, gpus INT64, egress_gbps INT64,
  kind STRING, disk_type STRING, topology STRING, cores INT64, memory_gb INT64, accelerator STRING);
CREATE NODE TABLE IF NOT EXISTS Shape(signature STRING PRIMARY KEY, type STRING, description STRING, vertices INT64, occurrences INT64);
CREATE REL TABLE IF NOT EXISTS Contains(FROM Vertex TO Vertex, count INT64);
CREATE REL TABLE IF NOT EXISTS HasShape(FROM Vertex TO Shape);
CREATE NODE TABLE IF NOT EXISTS Allocation(id STRING PRIMARY KEY, offer STRING, nodes INT64, from_time TIMESTAMP, to_time TIMESTAMP, request STRING, note STRING);
`

// AllocationColumns lists Allocation's columns in schema order.
const AllocationColumns = "id, offer, nodes, from_time, to_time, request, note"

// SaveAllocations is the statement that writes an existing database's Allocation table
// to file, for load to restore after it rebuilds the database.
func SaveAllocations(file string) string {
	return fmt.Sprintf("COPY (MATCH (a:Allocation) RETURN a.id AS id, a.offer AS offer, a.nodes AS nodes, a.from_time AS from_time, a.to_time AS to_time, a.request AS request, a.note AS note) TO '%s';\n", file)
}

// RestoreAllocations loads a file SaveAllocations wrote.
func RestoreAllocations(file string) string {
	return fmt.Sprintf("COPY Allocation FROM '%s';\n", file)
}

// LoadScript copies the Parquet tables from dir into the schema.
func LoadScript(dir string) string {
	return fmt.Sprintf(`COPY Vertex FROM '%[1]s/vertices.parquet';
COPY Shape FROM '%[1]s/shapes.parquet';
COPY Contains FROM '%[1]s/edges.parquet';
COPY HasShape FROM (LOAD FROM '%[1]s/vertices.parquet' RETURN id, signature);
`, dir)
}
