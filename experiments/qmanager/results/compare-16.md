## single phase, allocate_orelse_reserve

| | fluxion | graphdb |
|---|---|---|
| ALLOCATED | 24 | 24 |
| EINVAL | 1 | 0 |
| ENODEV | 8 | 9 |
| median latency | 1.9 ms | 20.8 ms |
| p95 latency | 99.3 ms | 25.2 ms |
| max latency | 99.4 ms | 115.2 ms |
| wall | 0.5 s | 0.8 s |
| memory | 704 MB | 176 MB |

Same outcome (placed or not) on 31 of 33 jobs; same machine type on 3 of the 23 both placed.

| job | request | fluxion | graphdb |
|---|---|---|---|
| s001-cores | 1 cores | m7i.xlarge | c6a.large |
| s002-cores | 4 cores | m7i.8xlarge | c6a.2xlarge |
| s003-cores | 8 cores | m7i.8xlarge | c6a.4xlarge |
| s004-cores | 16 cores | m7i.8xlarge | c6a.8xlarge |
| s005-cores | 32 cores | m7i.48xlarge | c6a.16xlarge |
| s006-cores | 64 cores, on one socket | m6g.16xlarge | c7g.16xlarge |
| s007-cores | 96 cores, on one socket | g6.48xlarge | c6a.48xlarge |
| s010-cores-2s | 64 cores, across 2 sockets | c7i.48xlarge | c7g.16xlarge |
| s014-memory | 8 cores, 64 GB memory | m7i.8xlarge | m7i.4xlarge |
| s015-memory | 8 cores, 256 GB memory | m7i.24xlarge | m7i.16xlarge |
| s016-memory | 8 cores, 512 GB memory | g6.48xlarge | m7i.48xlarge |
| s020-gpu | 4 cores, 1 GPUs, on one socket | g6.48xlarge | g6.2xlarge |
| s021-gpu | 8 cores, 2 GPUs, on one socket | g6.48xlarge | g6.12xlarge |
| s022-gpu | 16 cores, 4 GPUs, on one socket | g6.48xlarge | g6.12xlarge |
| s025-nodes | 2 nodes, each, 8 cores | m7i.4xlarge | c6a.4xlarge |
| s026-nodes | 4 nodes, each, 8 cores | m7i.16xlarge | c6a.4xlarge |
| s027-nodes | 8 nodes, each, 8 cores | m7i.12xlarge | c6a.4xlarge |
| s028-nodes | 16 nodes, each, 8 cores | m6g.8xlarge | c6a.4xlarge |
| s029-nic | 8 cores, a 32 Gbps NIC | hpc7g.8xlarge | hpc7g.4xlarge |
| s031-nic | 8 cores, a 200 Gbps NIC | hpc7g.16xlarge | hpc7g.4xlarge |

## stream phase, allocate_orelse_reserve

| | fluxion | graphdb |
|---|---|---|
| ALLOCATED | 166 | 191 |
| ENODEV | 9 | 9 |
| RESERVED | 25 | 0 |
| median latency | 1.7 ms | 22.1 ms |
| p95 latency | 188.8 ms | 31.5 ms |
| max latency | 385.1 ms | 126.1 ms |
| wall | 4.1 s | 5.2 s |
| memory | 704 MB | 177 MB |

Same outcome (placed or not) on 200 of 200 jobs; same machine type on 21 of the 191 both placed.

| job | request | fluxion | graphdb |
|---|---|---|---|
| t003-memory | 8 cores, 512 GB memory | g6.48xlarge | m7i.48xlarge |
| t004-cores | 8 cores | m7i.8xlarge | c6a.4xlarge |
| t005-cores | 1 cores | m7i.xlarge | c6a.large |
| t007-cores | 64 cores, on one socket | m6g.16xlarge | c7g.16xlarge |
| t008-cores | 8 cores | m7i.8xlarge | c6a.4xlarge |
| t009-cores | 64 cores, on one socket | hpc7g.16xlarge | c7g.16xlarge |
| t010-gpu | 16 cores, 4 GPUs, on one socket | g6.48xlarge | g6.12xlarge |
| t011-cores | 1 cores | m7i.large | c6a.large |
| t012-gpu | 16 cores, 4 GPUs, on one socket | g6.48xlarge | g6.12xlarge |
| t013-cores | 4 cores | m7i.4xlarge | c6a.2xlarge |
| t014-nodes | 4 nodes, each, 8 cores | m7i.48xlarge | c6a.4xlarge |
| t015-nodes | 2 nodes, each, 8 cores | m7i.24xlarge | c6a.4xlarge |
| t016-memory | 8 cores, 64 GB memory | m7i.16xlarge | m7i.4xlarge |
| t018-nodes | 8 nodes, each, 8 cores | m7i.12xlarge | c6a.4xlarge |
| t020-gpu | 16 cores, 4 GPUs, on one socket | g6.48xlarge | g6.12xlarge |
| t021-cores | 32 cores | m6g.8xlarge | c6a.16xlarge |
| t024-cores | 32 cores | m6g.12xlarge | c6a.16xlarge |
| t025-nodes | 2 nodes, each, 8 cores | m6g.4xlarge | c6a.4xlarge |
| t026-nodes | 4 nodes, each, 8 cores | m6g.2xlarge | c6a.4xlarge |
| t027-nodes | 2 nodes, each, 8 cores | hpc7g.8xlarge | c6a.4xlarge |
| t028-nodes | 4 nodes, each, 8 cores | hpc7g.4xlarge | c6a.4xlarge |
| t029-cores | 1 cores | m7i.2xlarge | c6a.large |
| t030-cores | 64 cores, on one socket | m6g.16xlarge | c7g.16xlarge |
| t031-nodes | 2 nodes, each, 8 cores | g6.8xlarge | c6a.4xlarge |
| t032-nodes | 2 nodes, each, 8 cores | g6.4xlarge | c6a.4xlarge |
| t033-cores | 8 cores | g6.24xlarge | c6a.4xlarge |
| t034-nodes | 8 nodes, each, 8 cores | g6.16xlarge | c6a.4xlarge |
| t035-nodes | 8 nodes, each, 8 cores | g6.12xlarge | c6a.4xlarge |
| t036-cores | 1 cores | m6g.xlarge | c6a.large |
| t037-nodes | 8 nodes, each, 8 cores | c7i.8xlarge | c6a.4xlarge |
| t038-cores | 1 cores | m6g.large | c6a.large |
| t040-gpu | 4 cores, 1 GPUs, on one socket | g6.48xlarge | g6.2xlarge |
| t041-nodes | 2 nodes, each, 8 cores | c7i.4xlarge | c6a.4xlarge |
| t042-cores | 4 cores | g6.2xlarge | c6a.2xlarge |
| t043-nodes | 4 nodes, each, 8 cores | c7i.48xlarge | c6a.4xlarge |
| t045-cores | 32 cores | c7i.24xlarge | c6a.16xlarge |
| t046-gpu | 4 cores, 1 GPUs, on one socket | g6.48xlarge | g6.2xlarge |
| t047-cores | 4 cores | c7i.2xlarge | c6a.2xlarge |
| t048-cores | 4 cores | c7i.16xlarge | c6a.2xlarge |
| t050-cores | 8 cores | c7i.12xlarge | c6a.4xlarge |

130 more.

