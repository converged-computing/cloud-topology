package graphdb

import "fmt"

// Schema is the LadybugDB (Cypher) schema the tables load into: Vertex and Shape node
// tables, a Contains relationship carrying the folded count, and HasShape.
const Schema = `CREATE NODE TABLE IF NOT EXISTS Vertex(
  id INT64 PRIMARY KEY, provider STRING, graph STRING, level STRING, region STRING, zone STRING, offer STRING,
  type STRING, name STRING, path STRING, depth INT32, count INT64, vertices INT64, size INT64, unit STRING,
  exclusive BOOLEAN, signature STRING, parent_id INT64, properties STRING,
  machine_type STRING, family STRING, provisioning_model STRING, vcpus INT64, gpus INT64, egress_gbps INT64,
  kind STRING, disk_type STRING, topology STRING);
CREATE NODE TABLE IF NOT EXISTS Shape(signature STRING PRIMARY KEY, type STRING, description STRING, vertices INT64, occurrences INT64);
CREATE REL TABLE IF NOT EXISTS Contains(FROM Vertex TO Vertex, count INT64);
CREATE REL TABLE IF NOT EXISTS HasShape(FROM Vertex TO Shape);
`

// LoadScript copies the Parquet tables from dir into the schema.
func LoadScript(dir string) string {
	return fmt.Sprintf(`COPY Vertex FROM '%[1]s/vertices.parquet';
COPY Shape FROM '%[1]s/shapes.parquet';
COPY Contains FROM '%[1]s/edges.parquet';
COPY HasShape FROM (LOAD FROM '%[1]s/vertices.parquet' RETURN id, signature);
`, dir)
}
