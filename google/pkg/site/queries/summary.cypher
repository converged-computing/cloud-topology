// What the database holds: per graph and vertex type, the folded rows and the siblings
// they stand for (a row with count 96 is 96 identical vertices under one parent).
MATCH (v:Vertex)
RETURN v.graph AS graph, v.type AS type, count(*) AS rows, sum(v.count) AS folded_siblings
ORDER BY graph, rows DESC;
