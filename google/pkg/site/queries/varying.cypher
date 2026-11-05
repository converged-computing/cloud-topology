// Machine types whose node shape is not the same everywhere it appears: more than one
// signature across the loaded graphs (for example E2 on AMD versus Intel hosts, or the
// same type captured in two databases with different results).
MATCH (o:Vertex {type: 'offer'})-[:Contains]->(:Vertex {type: 'placement'})-[:Contains]->(n:Vertex {type: 'node'})-[:HasShape]->(s:Shape)
WITH o.machine_type AS machine_type, collect(DISTINCT s.signature) AS shapes, collect(DISTINCT o.graph) AS graphs
WHERE size(shapes) > 1
RETURN machine_type, size(shapes) AS shapes, graphs
ORDER BY machine_type;
