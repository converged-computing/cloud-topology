// Offers that can hold a pattern: a node with at least 8 GPUs and an RDMA or GPU NIC,
// under an offer that also has a Hyperdisk ML pool; with where each lives and how many
// nodes quota allows. Change the numbers and types to ask for something else.
MATCH (o:Vertex {type: 'offer'})-[:Contains]->(p:Vertex {type: 'placement'})-[:Contains]->(n:Vertex {type: 'node'})
MATCH (n)-[:Contains]->(nic:Vertex {type: 'nic'})
WHERE o.gpus >= 8 AND nic.kind IN ['rdma', 'tcpx', 'tcpxo']
MATCH (o)-[:Contains]->(pool:Vertex {type: 'hyperdisk_ml'})
RETURN o.region AS region, o.zone AS zone, o.machine_type AS machine_type, o.gpus AS gpus,
       o.egress_gbps AS egress_gbps, collect(DISTINCT nic.kind + ' x' + CAST(nic.count AS STRING) + ' @' + CAST(nic.size AS STRING) + 'Gbps') AS nics,
       p.count * n.count AS nodes, pool.size AS hyperdisk_ml_gb
ORDER BY zone, machine_type;
