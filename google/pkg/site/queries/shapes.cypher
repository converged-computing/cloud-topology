// Node shapes: each distinct node subtree, how widely it occurs, and which machine types
// carry it. A shape carried by several machine types is a motif; a machine type with
// several shapes is a type whose hardware varies.
MATCH (n:Vertex {type: 'node'})-[:HasShape]->(s:Shape)
MATCH (o:Vertex {type: 'offer'})-[:Contains]->(:Vertex {type: 'placement'})-[:Contains]->(n)
RETURN s.signature AS shape, s.description AS description,
       count(DISTINCT o.machine_type) AS machine_types,
       count(DISTINCT o.zone) AS zones, count(DISTINCT o.region) AS regions,
       sum(n.count) AS nodes,
       collect(DISTINCT o.machine_type)[0:6] AS examples
ORDER BY machine_types DESC, nodes DESC
LIMIT 25;
