# Next experiment: scheduling fidelity

Status: built and run twice on 2026-11-07 (`make fidelity`); the first run found the catalog-only core-unit defect in the graph builder, fixed before the second. Results in `projects/<project>/fidelity/fidelity.md`, the experiments README and the site's Scheduling page. Nothing here runs in the cloud. Cloud cost: $0.

## Question

Everything so far produced graphs. This experiment asks whether the graphs are good enough
for the thing they are for: does the level of detail in the graph change what Fluxion
decides, and what does each level cost Fluxion to hold and search?

The three levels describe the same region at increasing fidelity: *series* pools a machine
series' vCPUs, GPUs and memory as totals sized by quota; *coarse* has one pooled node per
machine type; *exact* places cores and GPUs on sockets as captured, NICs with bandwidth on
nodes, nodes in compact placement groups, and storage pools on offers. A scheduler on the
series graph cannot see that a GPU and its cores share a socket. A scheduler on the exact
graph pays for 2.6 million vertices. Which decisions does the missing detail change, and
how much does the detail cost?

## Hypotheses

- H1 (feasibility): a jobspec that the exact graph rejects, the coarse and series graphs
  sometimes accept, because they cannot express the constraint being violated. Expected
  for GPU-near-cores, NIC locality, and local SSD requests. Expected *not* to happen for
  plain core and memory counts.
- H2 (placement): for jobs all three levels accept, the chosen machine type or zone
  differs between levels in a measurable fraction of cases, and the exact graph's choice
  is the one that honours the hardware constraint.
- H3 (packing): over a stream of jobs that fills the region, the levels place different
  numbers of jobs before the first failure, and the series graph over-commits because
  quota pools are not nodes.
- H4 (cost): load time and resident memory grow with vertex count; match time grows with
  graph depth more than with width. There is a nodes-per-offer size beyond which the exact
  graph is not practical for resource-query, and that size is a number we can state.
- H5 (ablation): of the exact graph's extras, only some change decisions. Removing
  placement groups changes multi-node placement; removing NIC local_socket changes
  nothing for the jobspecs that do not ask for a NIC; removing L3 domains changes nothing
  for Fluxion since Fluxion does not match on cache. This tells us what to keep.

## Method

Three inputs, all already on disk:

1. The captures: `projects/llnl-flux/agent/claude-opus-5-5/deterministic` (56 types) is the
   database; the consistency study rounds are available for a variance check.
2. The graphs: `make graphs` writes the full JGF (not the folded site version) per level
   and region. Sizes are controlled by `--nodes` (potential nodes per offer) and
   `--machine-types`, which is how H4's scale series is produced.
3. Fluxion: `resource-query` from the devcontainer's flux-sched build, driven in batch
   (`match allocate <jobspec>` lines on stdin, `-e` for elapsed time, `-F rv1` for the
   matched resource set).

The experiment is a Go command and a bash driver, in the experiments tree, never in the
production packages:

- `experiments/cmd/jobspecs` generates the workload from the catalog (below), one YAML per
  job per level, since each level needs the hierarchy spelled to its own depth. The
  generator is deterministic from a seed and the catalog, so the workload is reproducible.
- `experiments/scripts/fidelity.sh PLAN OUT` builds each graph variant (`make graphs`),
  runs each jobspec stream through resource-query per variant, and records per job: the
  level, variant, jobspec id, outcome (allocated or not), elapsed match time, and the
  matched vertex paths. Per variant it records load time and peak RSS (`/usr/bin/time -v`).
- `experiments/cmd/fidelity` reads the records and writes `fidelity.json` and
  `fidelity.md`: the feasibility matrix (H1), the placement agreement matrix (H2), the
  packing curves (H3), the scale table (H4), and the ablation deltas (H5).

Output layout follows the project convention:
`projects/<project>/fidelity/<variant>/` with variants `series`, `coarse`, `exact`,
`exact-no-placement`, `exact-no-nic`, `exact-no-l3`, and `exact-n<k>` for the scale series.

### Workload

Generated from the machine types the database holds, so every job is answerable by
something that exists:

| Family | Shape | Expected to separate |
|---|---|---|
| cores | 1, 4, 8, 16, 32, 64, 96, 176, 360 cores in one node | nothing (all levels agree) |
| memory | cores plus memory that only highmem/megamem/ultramem types satisfy | series vs others on machine type |
| gpu-near-cores | 1, 2, 4, 8 GPUs each with 4 cores on the same socket | exact vs coarse and series |
| multi-node | 2, 4, 8, 16 nodes in one placement group | exact vs others; ablation no-placement |
| nic | one node with a NIC of at least 100 or 200 Gbps | exact vs others; ablation no-nic |
| local-ssd | cores plus local SSD | exact vs others |
| stream | 200 jobs drawn from the families above, in a fixed random order, submitted until the first failure | packing per level |

### Scale series (H4)

The exact graph for us-central1 with every type at one node per offer, then
`--nodes` 2, 4, 8, 16, 32, 64 (quota caps apply). Per size: vertices, edges, JGF bytes,
resource-query load seconds, peak RSS, and median match time for the cores and
gpu-near-cores families. The run stops at the first size that fails to load in the
devcontainer (58 GB), and that is the reported limit.

### Ablations (H5)

Graph builder flags, added to `cmd/graph` for the experiment but harmless in production:
`--no-placement` (every node its own placement group), `--no-nic` (NICs omitted), `--no-l3`
(no cache domains). The folded signature code is untouched; ablations are graph-build
options only.

## What we measure

| Measure | Unit | From |
|---|---|---|
| feasibility | allocated or not, per job per level | resource-query output |
| placement | machine type, zone, placement group of the match | rv1 resource set |
| agreement | fraction of jobs where two levels choose the same machine type / zone | derived |
| packing | jobs placed before first failure, per level | stream |
| load time, RSS | seconds, MB per variant | /usr/bin/time -v |
| match time | ms per job | resource-query -e |

## Cost and safety

No cloud resources are created. The only cost is local CPU and memory in the devcontainer.
The driver must not call gcloud at all; the Makefile target that runs it sets no PROJECT.

## Runs

```bash
make fidelity                         # all variants, full workload, ~hours of local time
make fidelity VARIANTS=coarse,exact   # a subset
make fidelity-report                  # rebuild fidelity.md from the records
```

Expected wall clock: load of the full exact graph is the unknown; everything else is
minutes. The scale series is run last so a failure to load does not block the rest.

## What we learn

A table that says, for each kind of job, which graph level gets the answer right and what
it costs to use. That is the result a Flux deployment on cloud needs in order to choose a
level, and it is the first use of the captures for scheduling rather than description.
Also a stated upper bound on exact-graph size for resource-query, which decides whether the
exact level is a per-region graph or a per-offer graph in practice.

## After this

### Prerequisite for any further cloud run: guardrails

Written down because of 2026-11-06, when an abandoned GKE node pool ran an a3-highgpu-8g
node idle for 13 hours (about $690).

1. **Preflight.** Every experiment script starts by listing capture instances, node pools
   and clusters carrying our label, and refuses to run if any exist (FORCE=1 overrides).
2. **Lifetime by construction.** GCE instances already carry `--max-run-duration` with
   `--instance-termination-action=DELETE`, so a VM deletes itself even if the orchestrator
   dies. GKE node pools have no equivalent, so the gke script runs a watchdog subshell
   (killed on exit) that deletes any capture pool older than MAX_POOL_MINUTES (default 45),
   and the provider now waits for the create operation and confirms the pool is gone.
3. **Postflight audit.** Every script ends by listing the same resources and exits non-zero
   if anything is left, printing the delete commands.
4. **Price ceiling.** The plan carries an hourly price per type (from the pricing catalog
   SKUs, cached in `data/prices.json`); the runner refuses types above PRICE_CAP dollars
   per hour unless listed in ALLOW_EXPENSIVE, and prints the estimated spend of the run
   (types × price × expected minutes) before starting.
5. **Billing export.** BigQuery billing export enabled in the Console so spend is
   queryable from the shell the next day.

### Then: AWS

Status 2026-11-07: the data side is built (`aws/`): catalog, quotas, the three graph
levels for us-east-1 and the folded graphs loaded with Google's into one LadybugDB, all
from read-only API calls at no cost. Captures are the remaining step and the only one
that spends money.

The same plan on EC2, for cross-cloud motifs. Provider `aws` in the agent: catalog from
`describe-instance-types`, quotas from Service Quotas, largest size per family (c7i, c7a,
c7g, m7i, r7i, hpc7a, g5, g6, p4d, p5, trn1), Spot with capacity-optimized allocation, SSM
for exec so no SSH keys, `InstanceInitiatedShutdownBehavior=terminate` plus a
`shutdown -h +30` in user data as the lifetime guardrail. Estimated cost with Spot and the
30-minute cap: under $100 for the full plan. Design to follow once the fidelity experiment
has a result, since its result decides which graph level the AWS graphs need.

### Open question: agents on the cluster

"Agent Mesh" was raised earlier without a definition. The nearest concrete experiment is a
DaemonSet that captures every node of a live heterogeneous cluster and builds the cluster's
exact graph from the per-node captures, then checks it against the graph composed from the
per-type captures. Needs a definition of what the agents decide that the deterministic
DaemonSet would not; parked until that is written.
