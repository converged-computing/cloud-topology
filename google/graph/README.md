# graph

Builds Fluxion JGF resource graphs from the database.

```bash
go run ./graph --level coarse --region us-central1 --nodes 4 --out coarse.json
go run ./graph --level exact --region us-central1 --machine-types g2-standard-8 --nodes 2 --out exact.json
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
go run ./graph --level series --region us-central1 --quota projects/my-project/quota.json --out series.json
```

**Coarse** (per machine type): `/heuretes/gcp/<region>/<offer>/node<i>`, where each potential node
holds pooled `gpu`, `core` and `memory` counts. Offer properties include the zones offering it.

**Exact** (secretary): `/gcp/<region>/<zone>/<offer>/node<i>/socket<j>/...` with individual
cores, GPUs and NICs placed on the socket they are local to. The offer's `topology` property says
where each node's layout came from:

- `hwloc`: captured on this machine type.
- `derived:<machine type>`: cut from the smallest captured, at-least-as-large member of the same
  series. Cores are taken socket by socket from socket 0, GPUs first from those sockets, and
  memory is split across them. This assumes a smaller VM sees a slice of the larger one's
  layout, which is unverified; capture the smallest size too (`make plan-topology VERIFY=1`)
  and compare.
- `catalog`: no capture in the series; one socket with a core per vCPU.

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
