# aws

The EC2 side of the two-cloud graph: the same static database, quota snapshot and
Fluxion resource graphs as `google/`, built from the EC2 API, folded into the same
LadybugDB so one Cypher query spans both clouds. Its own Go module; it imports the
provider-neutral pieces of `google/` (the JGF graph type, hwloc parsing, folding, the
graph database export) and keeps everything EC2-specific here.

The catalog, quota, price and graph commands are read-only API calls or run on disk.
Only `make topology` and `make capture-all` create instances, one at a time, with the
guardrails described under Captures.

## Setup

The AWS CLI v2 is in the devcontainer. Credentials come from `~/.aws` or the environment
(`aws configure`, or `aws configure sso` and `aws sso login`); read access to EC2 and
Service Quotas is enough for everything here.

```bash
aws sts get-caller-identity          # who am I
make catalog REGION=us-east-1        # instance types and EBS volume types into data/
make quota REGION=us-east-1          # EC2 and EBS quotas into projects/<account>/quota.json
make plan                            # nodes per instance type the quotas allow
make graphs                          # series, coarse and exact JGF into projects/<account>/
make folded                          # the folded graphs for the graph database
make test                            # unit tests, no API calls
```

At the repository root, `make graphdb` folds the AWS graphs, builds the Google ones and
loads both into `google/graphdb/graph.lbug`; `make graphdb-query Q=providers` runs the
shipped cross-cloud query.

## What the EC2 catalog gives that the Google catalog does not

`describe-instance-types` reports, per instance type: physical cores and threads per
core; each network card with its baseline and peak bandwidth and whether it accepts an
EFA interface; instance store volumes with size, count and SSD or HDD; EBS-optimized
bandwidth and IOPS; supported placement strategies; purchasing options (On-Demand, Spot,
Capacity Blocks); accelerator name, manufacturer and memory. The Google module encodes
most of that from documentation tables (`google/pkg/network`, `google/pkg/storage`); here
it is read from the API and stored in `data/machine-types/<type>.json`.

What EC2 does not report is the socket layout: a catalog-built node has the right number
of cores (not a guess from vCPUs) but one socket, and the offer says `topology=catalog`.
Captures (`topology/<type>/hwloc.xml`, same format as Google's) replace that when they
exist; sizes without a capture are derived from the largest captured size in their
series as on Google. `data/topology/` holds 143 captures from 2026-11-08, the largest
size of every series in us-east-1 except g6e, g7e and p4de, which had no capacity in any
zone over several hours (g6 and r8id are captured one size down). The run cost about
$104; the plan, its estimate and the guardrails are in
[design/captures.md](design/captures.md).

hwloc lists NVIDIA and AMD accelerators as PCI display devices, so the captures carry
them with bus ids; AWS Inferentia and Trainium chips do not appear on the PCI bus at
all, so for inf and trn types the exact graph adds the catalog's accelerators on the
node with `source=catalog`.

## Captures

`topology-agent` gained an `aws` provider (`topology_agent/providers/aws.py`): one EC2
instance per capture in the account's default VPC, Ubuntu 24.04 from the SSM public
parameter for the type's architecture, reached over SSH with a key generated for the run
through a security group that admits only this host's IP. The guardrails from
[design/captures.md](design/captures.md) are in the code, not the instructions:

- every launch carries `--instance-initiated-shutdown-behavior terminate` and user data
  whose first line is `shutdown -h +30`, so the instance ends itself whatever happens
  here (`LIMIT_MINUTES`); Spot requests are one-time;
- `delete` waits until EC2 reports the instance terminated, so the runner never has two;
- the runner refuses to start while a tagged instance exists, and ends by listing
  tagged instances and volumes, terminating and failing loudly if any remain;
- the plan carries each type's hourly price and leaves out types above `PRICE_CAP`
  (default $25/h) unless named in `ALLOW_EXPENSIVE`; its header states the expected and
  the bounded cost; `DRY_RUN=1` on a capture has EC2 validate the launch without
  creating anything, and on the plan prints its lines.

```bash
make prices                                   # hourly prices into projects/<account>/prices.json
make plan-topology                            # the plan with its estimate; review it
make topology MACHINE_TYPE=t3.2xlarge ZONE=us-east-1a SPOT=1 DRY_RUN=1   # EC2 validates, launches nothing
make topology MACHINE_TYPE=t3.2xlarge ZONE=us-east-1a SPOT=1             # one real capture, watched
make capture-all                              # the plan, one instance at a time, logged
make cleanup                                  # tagged instances still existing; DELETE=1 terminates
make budget BUDGET_USD=300 BUDGET_EMAIL=you@example.com                  # the billing backstop
```

A capture takes two to twelve minutes; bare-metal types (`METAL=1`) boot slowly and are
off by default. Captures land in `data/topology/<type>/` and the next `make graphs`,
`make folded` and the root `make site` use them. A type that fails or finds no capacity
joins a retry set and is tried again after the other captures; the run never stops on
failures. `make capture-all` plans only series without a capture, so rerunning it later
picks up what stocked out.

## Layout

- `data/`: the database. `machine-types/<type>.json` keeps the Google field names for the
  common fields (name, family, vcpus, memory_mb, accelerators, architecture, zones) and
  adds the EC2 ones (cores, threads_per_core, network, instance_store, ebs,
  placement_strategies, usage_classes, bare_metal, hypervisor). `disk-types/` holds the
  six current EBS volume types with the documented size and performance limits
  (`pkg/storage`). `zones.json` maps this account's zone names to zone ids, since names
  differ by account.
- `projects/<account>/`: the quota snapshot, the graphs and the folded graphs; ignored by
  git like `google/projects/`.
- `pkg/catalog`, `pkg/quota`, `pkg/storage`, `pkg/jgf`, `pkg/database`: the EC2 versions
  of the Google packages. `pkg/quota` maps an instance family to its Service Quotas class
  (Standard, P, G and VT, HPC, Trn, Inf, X, High Memory, F, DL) and reads the On-Demand
  and Spot vCPU limits; Service Quotas reports limits only, so node counts are upper
  bounds and `make plan` prints them as `<=`.

## Graphs

The three levels mirror Google's exactly, with the same vertex types and offer
properties, so the fidelity jobspecs and the Cypher queries apply unchanged. Both clouds
hang off the same root, `site0`, with one provider vertex each, so the site and the graph
database hold them as one tree:

- series: `/site0/aws/<region>/<family>.<model>` with pooled core, gpu and memory
  vertices sized by the class quota. A family (c7i) is a series; bare-metal sizes form
  their own (c7i.metal).
- coarse: `/site0/aws/<region>/<type>.<model>/node<i>` with pooled core, gpu, memory,
  one nic carrying the summed egress, and one local_ssd or local_hdd per instance store
  volume; `ebs_<type>` pools on the offer sized by the EBS quota.
- exact: `/site0/aws/<region>/<zone>/<type>.<model>/placement<k>/node<i>/socket<j>/...` with
  one nic per network card (kind `efa` for the cards that take an EFA, `ena` otherwise,
  sized at peak Gbps), instance store volumes on the node, EBS pools on the offer. Types
  that support the cluster placement strategy put all their nodes in one placement
  group; others get groups of one. Zones carry their zone id.

Models: `on-demand` by default; `MODELS=on-demand,spot` adds Spot offers where the type
has the usage class, sized by the Spot quota.

us-east-1 on 2026-11-07 (1,244 current-generation types, 159 families, six zones):

| Graph | Vertices | Notes |
|---|---|---|
| series | 458,518 | one offer per family and model |
| coarse | 374,553 | up to 64 nodes per type |
| exact (file, 4 nodes per offer) | 1,037,060 | 518 MB; `EXACT_MAX_NODES` |
| exact (folded, 64 nodes per offer) | 15,491,913 | 17 MB folded; 1.2 s to build; captured layouts |

The class quotas are large (32,768 On-Demand Standard vCPUs) and shared by every type in
the class, so sizing each type independently gives nearly every offer the 64-node cap;
the exact file is capped lower so Fluxion can load it, and the folded graph keeps the
full counts. Twenty-six `mac` types are left out: they run on Dedicated Hosts and have
no vCPU quota.
