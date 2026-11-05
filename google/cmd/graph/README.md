# graph

Builds Fluxion JGF resource graphs from the database.

```bash
go run ./cmd/graph --level coarse --region us-central1 --nodes 4 --out coarse.json
go run ./cmd/graph --level exact --region us-central1 --machine-types g2-standard-8 --nodes 2 --out exact.json
```

**Series** (hyperscheduler, matching on totals): `/heuretes/gcp/<region>/<series>.<model>`,
where a series is a machine family and type such as `n2-standard` or `g2-standard`; bare-metal
types form their own series, such as `c3-standard-metal`. Each offer
holds one `core` vertex per vCPU and one `gpu` vertex per GPU that quota allows, and memory in
chunks of the series' smallest GB-per-vCPU. Requests ask for totals, and a lower level chooses
the machine sizes (`quota sizes`). For GPU series, which need GPU quota only, vCPUs are GPUs times
the most vCPUs per GPU in the series, and memory uses the series' largest GB-per-vCPU, so the
totals are upper bounds. Without `--quota`, each offer holds `--nodes` of the largest size.
Units are individual vertices because Fluxion v0.56.0 hands a pooled vertex to a request whole;
`--max-vcpus` and `--max-gpus` defaults (1024 and 64) bound the graph size.

```bash
go run ./cmd/graph --level series --region us-central1 --quota projects/my-project/quota.json --out series.json
```

**Coarse** (per machine type): `/heuretes/gcp/<region>/<offer>/node<i>`, where each potential node
holds pooled `gpu`, `core` and `memory` counts. Offer properties include the zones offering it.

**Exact** (secretary): `/gcp/<region>/<zone>/<offer>/placement<k>/node<i>/socket<j>/...` with
individual cores and GPUs placed on the socket they are local to. NICs sit on the node, so
every node has the same shape for a jobspec (`node > nic`); the socket hwloc saw a NIC local
to, when it saw one, is its `local_socket` property. The offer's `topology`
property says where each node's layout came from:

- `hwloc`: captured on this machine type.
- `derived:<machine type>`: cut from the smallest captured, at-least-as-large member of the same
  series. Cores are taken socket by socket from socket 0, GPUs first from those sockets, and
  memory is split across them. This assumes a smaller VM sees a slice of the larger one's
  layout, which is unverified; capture the smallest size too (`make plan-topology VERIFY=1`)
  and compare.
- `catalog`: no capture in the series; one socket with a core per vCPU.

**Network and placement.** The Compute Engine API says nothing about bandwidth or placement,
so `pkg/network` encodes Google's documentation (the network bandwidth, GPU network
bandwidth and placement policy pages, each cited in the source, last checked 2026-11-05):

- Every `nic` vertex carries its egress bandwidth as its size in Gbps, from the series'
  per-vCPU rule and cap, and a `kind`: `gvnic`, or for the accelerator-optimized types
  with documented layouts `tcpx`, `tcpxo` and `rdma` (A3 Ultra, A4 and A4X have eight
  400 Gbps RDMA NICs, marked `rdma=true`). A capture only sees the instance's default
  interface, so where a layout is documented the rule's NICs replace it, the first placed
  where the capture saw it and the rest at the node. Offers carry `egress_gbps`,
  `tier1_gbps` where Tier_1 networking exists, and the `bandwidth_rule` used. The coarse
  graph pools the same bandwidth into one `nic` per node.
- A `placement` level between offer and node models compact placement policies. Series
  that support them get groups of the documented size, 1,500 at the default unspecified
  max distance (`--placement-distance 1` gives groups of 22, `2` of 150, or 256 for A3,
  `3` of 1,500, each for the series that support that distance); other series get groups of
  one node. A jobspec that asks for several nodes under one `placement` therefore matches
  only where Google can place them together, and a jobspec that wants nodes anywhere asks
  for that many groups of one. The offer's `placement` property states the rule applied.

**Storage.** Each offer in the exact and coarse graphs holds one pool per disk type its
machine type can attach among the types offered in its zone (`pd_standard`, `pd_balanced`,
`pd_ssd`, `pd_extreme`, `hyperdisk_balanced`, `hyperdisk_extreme`, `hyperdisk_throughput`,
`hyperdisk_ml`), sized in GB. Which series attach which types comes from `pkg/storage`,
encoded from the Persistent Disk and Hyperdisk machine series support tables (last checked
2026-11-05): C4, C4A, C4D, N4, X4 and M4 attach only Hyperdisk, C3, C3D and Z3 attach
Balanced and SSD PD plus Hyperdisk, and so on. The disk types themselves come from the
catalog (`data/disk-types/`, exported by `make catalog`). With `--quota`, Standard PD pools
hold the region's `DISKS_TOTAL_GB` and the SSD-backed PD types its `SSD_TOTAL_GB` (shared
across the types and the offers, like node counts); Hyperdisk quotas are not fetched yet, so
those pools hold one largest disk and say so in their `capacity` property. A jobspec asks
for storage as a count of GB under the offer (see `examples/jobspecs/exact-8-cores-ssd.yaml`),
so it can only land on an offer whose machine type attaches that disk type. Local SSD stays
a per-node resource.

Variants are represented rather than dropped. Offers carry `metal=true` for bare metal,
`shared_cpu=true` for shared-core types and `local_ssd=true` for Local SSD types, with
`local_ssd_partitions` and `local_ssd_interface` from the catalog. In the coarse and exact graphs,
each Local SSD partition is also a `local_ssd` vertex under its node, so a jobspec can request it.
Series offers list their Local SSD sizes in `local_ssd_sizes`, and bare-metal series carry
`metal=true`.

An offer is a machine type with a provisioning model (`--models`, default `on-demand`). With
`--quota projects/<project>/quota.json`, each offer gets the number of nodes the project's quota
allows (capped by `--max-nodes`, default 64), and offers the project cannot run are left out.
Without it, every offer gets `--nodes` potential nodes.

Quota is regional. The exact graph gives every zone's offer the full regional count, because any
one zone could host all of it; Fluxion does not enforce the regional total across zones, so the
secretary must check quota before committing.
Fluxion carries offer properties but does not match on them; ranking by properties belongs to
the layer above.

Notes from testing with Fluxion v0.56.0: jobspecs must spell out every level from the root (see
`examples/jobspecs`), and offers that are unavailable should be marked down at runtime
(`set-status`) rather than with a `status` field in the JGF.
