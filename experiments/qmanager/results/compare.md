## single phase, allocate_orelse_reserve

| | fluxion | graphdb |
|---|---|---|
| ALLOCATED | 22 | 22 |
| EINVAL | 1 | 0 |
| ENODEV | 10 | 11 |
| median latency | 1.3 ms | 19.1 ms |
| p95 latency | 28.3 ms | 31.9 ms |
| max latency | 35.3 ms | 110.4 ms |
| wall | 0.2 s | 0.8 s |
| memory | 245 MB | 178 MB |

Same outcome (placed or not) on 31 of 33 jobs; same machine type on 3 of the 21 both placed.

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
| s029-nic | 8 cores, a 32 Gbps NIC | hpc7g.8xlarge | hpc7g.4xlarge |
| s031-nic | 8 cores, a 200 Gbps NIC | hpc7g.16xlarge | hpc7g.4xlarge |

## stream phase, allocate_orelse_reserve

| | fluxion | graphdb |
|---|---|---|
| ALLOCATED | 154 | 179 |
| ENODEV | 21 | 21 |
| RESERVED | 25 | 0 |
| median latency | 1.2 ms | 22.7 ms |
| p95 latency | 44.6 ms | 33.0 ms |
| max latency | 94.4 ms | 120.8 ms |
| wall | 1.3 s | 5.1 s |
| memory | 245 MB | 179 MB |

Same outcome (placed or not) on 200 of 200 jobs; same machine type on 25 of the 179 both placed.

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
| t020-gpu | 16 cores, 4 GPUs, on one socket | g6.48xlarge | g6.12xlarge |
| t021-cores | 32 cores | m6g.8xlarge | c6a.16xlarge |
| t024-cores | 32 cores | m6g.12xlarge | c6a.16xlarge |
| t025-nodes | 2 nodes, each, 8 cores | m7i.12xlarge | c6a.4xlarge |
| t026-nodes | 4 nodes, each, 8 cores | m6g.4xlarge | c6a.4xlarge |
| t027-nodes | 2 nodes, each, 8 cores | m6g.2xlarge | c6a.4xlarge |
| t028-nodes | 4 nodes, each, 8 cores | hpc7g.8xlarge | c6a.4xlarge |
| t029-cores | 1 cores | m7i.2xlarge | c6a.large |
| t030-cores | 64 cores, on one socket | m6g.16xlarge | c7g.16xlarge |
| t031-nodes | 2 nodes, each, 8 cores | hpc7g.4xlarge | c7g.2xlarge |
| t032-nodes | 2 nodes, each, 8 cores | g6.8xlarge | c7g.2xlarge |
| t033-cores | 8 cores | g6.4xlarge | c7g.2xlarge |
| t036-cores | 1 cores | m6g.xlarge | c6a.large |
| t038-cores | 1 cores | m6g.large | c6a.large |
| t040-gpu | 4 cores, 1 GPUs, on one socket | g6.48xlarge | g6.2xlarge |
| t041-nodes | 2 nodes, each, 8 cores | g6.24xlarge | c7g.2xlarge |
| t042-cores | 4 cores | g6.2xlarge | c6a.2xlarge |
| t043-nodes | 4 nodes, each, 8 cores | g6.16xlarge | c7g.2xlarge |
| t045-cores | 32 cores | c7i.48xlarge | c6a.16xlarge |
| t046-gpu | 4 cores, 1 GPUs, on one socket | g6.48xlarge | g6.2xlarge |
| t047-cores | 4 cores | g6.12xlarge | c6a.2xlarge |
| t048-cores | 4 cores | c7i.8xlarge | c6a.2xlarge |
| t050-cores | 8 cores | c7i.4xlarge | c7g.2xlarge |
| t052-gpu | 8 cores, 2 GPUs, on one socket | g6.48xlarge | g6.12xlarge |
| t053-gpu | 16 cores, 4 GPUs, on one socket | g6.48xlarge | g6.12xlarge |
| t054-cores | 4 cores | c7i.2xlarge | c6a.2xlarge |
| t055-cores | 32 cores | c7i.24xlarge | c6a.16xlarge |

114 more.

