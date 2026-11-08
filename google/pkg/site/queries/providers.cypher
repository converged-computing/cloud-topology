// The same pattern across clouds: offers whose node holds at least 8 accelerators and an
// RDMA-class NIC (Google rdma, tcpx, tcpxo; AWS efa), per provider and zone, with the NIC
// layout and the nodes quota allows. Needs the AWS folded graphs loaded (make graphdb at
// the repository root).
MATCH (o:Vertex {type: 'offer'})-[:Contains]->(p:Vertex {type: 'placement'})-[:Contains]->(n:Vertex {type: 'node'})
MATCH (n)-[:Contains]->(nic:Vertex {type: 'nic'})
WHERE o.gpus >= 8 AND nic.kind IN ['rdma', 'tcpx', 'tcpxo', 'efa']
RETURN o.provider AS provider, o.zone AS zone, o.machine_type AS machine_type, o.gpus AS gpus, o.vcpus AS vcpus,
       o.egress_gbps AS egress_gbps,
       collect(DISTINCT nic.kind + ' x' + CAST(nic.count AS STRING) + ' @' + CAST(nic.size AS STRING) + 'Gbps') AS nics,
       p.count * n.count AS nodes, o.topology AS topology
ORDER BY provider, machine_type, zone;
