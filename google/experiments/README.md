# Experiments

Experiments run captures to answer a question and write what they find under
`projects/<project>/<experiment>/<variant>/`: `agent/<model>/` for the agent-versus-runner
experiment, `gke/<node image>/` for the GKE experiment, `consistency/` for the study. They are kept apart from the production code that builds, queries
and serves the database: nothing here is imported by `cmd/` or `pkg/`, and the production
site only displays the reports these write.

- `cmd/report`, `cmd/consistency` and `cmd/latency` compute the reports.
- `pkg/report`, `pkg/consistency` and `pkg/latency` are their libraries, tested against
  `testdata/`.
- `scripts/` orchestrates runs in bash, since each capture is a call to the Python
  topology-agent client; `topology/run-plan.sh` in the production tree does the capturing.
- `Makefile` holds the targets; `make -C experiments <target>` or `make <target>` from
  `google/` both work, and `PROJECT`, `REGION` and the rest pass through.

## Experiment: the agent against a deterministic runner

The experiment runs one plan twice, from the same starting state, to compare success rates
with and without flexibility: first topology-agent's agent works the whole plan (it decides
how to react to stockouts and errors within a bounded tool), then `topology/run-plan.sh`
works the same plan with fixed rules (zones in order, stockouts retried after a wait, quota
failures not retried). Each arm captures into its own database, so neither sees the other's
captures, and the plan includes series that `data/` already holds.

Prerequisites are the catalog and quota snapshot from above, topology-agent, and for the
agent arm a Claude login (`claude`, then `/login`; the devcontainer keeps it on a volume).
Then:

```bash
export PROJECT=my-project
export REGION=us-central1

make experiment-test REGION=$REGION           # try it on the 3 cheapest machine types first
make experiment REGION=$REGION                # both arms over the whole plan, then the report
make experiment REGION=$REGION MODEL=opus     # pick the agent's model (BACKEND=claude|gemini|aws)
make report                                   # rebuild the report from what is on disk
make report FORMAT=json
```

`experiment-test` is the same run on a short plan (`LIMIT`, default 3 lines) in
`projects/$PROJECT/agent/test/`, with stockouts retried after 5 minutes instead of 30,
to check logins, quota and the agent before committing to the full plan. `make report
EXPERIMENT=projects/$PROJECT/agent/test` reads its result.

Everything lands in `projects/$PROJECT/agent/<model>/`, one directory per agent model (`MODEL`, default `claude-opus-5-5`); `MODEL=claude-opus-5-5` runs and files the same experiment with Opus:

| File | What it is |
| --- | --- |
| `captures.txt` | the plan both arms ran (`make plan-topology RECAPTURE=1`); kept across reruns, delete it for a new plan |
| `agent/` | the agent arm's database: `topology/<type>/{capture.json,hwloc.xml,topology.svg}` and `index.json`, with `machine-types` linked from `data/` |
| `agent-result.json`, `agent.log` | the agent's messages and log |
| `deterministic/` | the runner's database, laid out the same way |
| `deterministic-captures.log` | one line per attempt: time, machine type, `ok`/`stockout`/`quota`/`error`, seconds, pass, error message |
| `phases.tsv` | each arm's start, end, seconds and exit status |
| `report.md`, `report.json` | the comparison: per-arm captured counts and rates, wall clock, mean provisioning time, what either arm got, and a per-machine-type table |

Each arm creates one VM per plan line, a few minutes each when it succeeds, so a 57-line plan
takes three to four hours per arm with the retry waits. Run it in a terminal you keep open;
the scripts print progress and keep their own logs in the experiment directory.

Knobs, all optional: `RETRY_WAIT` (seconds before stocked-out captures are retried, default
1800) and `RETRY_PASSES` (default 2) apply to both arms, so the agent may retry a stocked-out
type as often, and wait as long, as the runner; `MAX_ATTEMPTS` caps the agent's attempts in
all (default one try plus the retries per type); `STOP_AFTER_ERRORS` (consecutive distinct
errors that stop the runner, default 5) is the deterministic arm's; `ON_DEMAND=1`, `VERIFY=1`, `METAL=1` and
`SHARED_CORE=1` shape a new plan. A finished arm is skipped on rerun, so `make experiment`
after an interruption continues; `ARMS=deterministic FORCE=1` starts one arm over, clearing
its captures and log. To keep a finished arm's results before starting it over, move its
directory, log, `.done` marker and result file into a subdirectory first, for example
`archive/<date>/`; the site and report ignore subdirectories they do not know. Capture instances an arm leaves running are deleted before the next
arm starts (`KEEP_LEFTOVERS=1` keeps them). `make cleanup` lists any that remain.

Reading the report: `either` is the rate the two approaches reach together, and the per-type
table shows where they differ. Spot stock changes by the hour, so run the arms back to back,
and treat a single run as one sample. The agent records no per-type reason for a miss, so
its misses read `not captured`; the reasons are in `agent-result.json` and `agent.log`. To
use an arm's captures afterwards, point the other commands at it
(`make graphs DATA=projects/$PROJECT/agent/claude-opus-5-5/agent GRAPHS=projects/$PROJECT/agent/claude-opus-5-5/agent`) or copy `topology/<type>` directories
into `data/` and run `make index`.

## Consistency study

Is a machine type's hardware the same every time? `make consistency` captures the whole
plan repeatedly, in rounds spread over days, each round into its own database under
`projects/$PROJECT/consistency/rounds/<timestamp>/`, with the zone order rotated by one
place each round so a type is seen in different zones. The deterministic runner does the
capturing; the study is about the hardware, not the strategy.

```bash
export PROJECT=my-project REGION=us-central1
make consistency REGION=$REGION                                   # 6 rounds, 12 hours apart, in a terminal kept open
make consistency REGION=$REGION ROUNDS=4 INTERVAL=21600           # 4 rounds, 6 hours apart
make consistency-report EXTRA_DATA="main=data vm=projects/$PROJECT/agent/claude-opus-5-5/deterministic"
```

A round takes as long as `make capture-all` (about two hours for 57 types, plus retry
waits) and costs the same; the full study is one VM per type per round. A restarted study
continues from the rounds already on disk. After every round the report is rebuilt in
`consistency.md` and `consistency.json`: for each machine type, how many captures and how
many distinct shapes, which fields vary (sockets, cores, PUs, NUMA nodes, memory, L3 and L2
sizes, CPU model, GPU and NIC placement), and for varying types every capture with its
zone, model, time, kernel and hwloc version. `make site STUDY=projects/$PROJECT/consistency`
adds the rounds to the web interface, whose Consistency view shows the same.

## GKE experiment: does a Kubernetes node see the same hardware, and what does the path cost?

A GKE node is a VM of the same machine type, so its captured topology should match the
bare VM's. Two things could make it differ: the node image (Container-Optimized OS by
default, against the Ubuntu image the VM captures boot) and the container the capture runs
in, which hwloc must be told to look past. And the Kubernetes path has its own cost: a
cluster to create, a node pool to bring up, a pod to admit. The experiment captures the
plan on one-node node pools of a GKE cluster and compares with the VM captures.

```bash
export PROJECT=my-project REGION=us-central1
make gke REGION=$REGION LIMIT=3                     # try three types first
make gke REGION=$REGION                             # the whole plan, about as long as a VM pass plus cluster time
make gke REGION=$REGION IMAGE_TYPE=UBUNTU_CONTAINERD  # the Ubuntu node image, filed under gke/ubuntu_containerd
make gke-report                                     # rebuild the reports against the VM captures
```

What happens: `scripts/gke.sh` creates a zonal cluster named `CLUSTER` (default `topo`) in
the plan's first zone with no default nodes, timing it, then runs the plan through
`topology/run-plan.sh` with `PROVIDER=gke`. For each machine type topology-agent's `gke`
provider creates a one-node node pool in the zone (Spot where the plan says so), waits for
the node, runs hwloc in a privileged pod pinned to it, writes the capture into
`projects/$PROJECT/gke/<image>/gke/`, and deletes the pool. The capture's `provision_seconds` is
the node pool plus pod time, and the record carries the provider, node image and the pod
and node pool timings.

A stocked-out node pool is not free to walk away from: gcloud stops waiting before GKE
does, and the pool's instance group keeps trying to get a node for as long as the pool
exists. The provider therefore waits for the create operation to finish, deletes the pool
(waiting out any operation the cluster is busy with) and confirms it is gone before it
reports the attempt, and takes the outcome from the operation so a stockout moves on to the
next zone. The script deletes the cluster however the run ends (normally, on an error, or on
Ctrl-C), after removing any capture pool still present, and retries the deletion while the
cluster is busy. If the cluster still cannot be deleted it exits non-zero and prints the
command to run. `KEEP_CLUSTER=1` keeps the cluster; check `gcloud container clusters list`
afterwards. A rerun of `make gke`
captures only the types still missing (the runner skips types already captured, and
retries errors as well as stockouts in its retry passes), so an interrupted or partly
failed run is finished by running it again.

The reports land next to the captures: `consistency.md` compares each type's fingerprint
with the VM captures in `COMPARE` (default the deterministic arm of the agent experiment for `MODEL`) and
names any field that differs, kernel and hwloc version recorded separately so a changed
image can be told from changed hardware; `latency.md` compares provisioning time per type
and the cluster creation time, so an offer can be given a `setup_seconds` for each
provisioning path. `make site SITE_DATA="vm=projects/$PROJECT/agent/claude-opus-5-5/deterministic gke=projects/$PROJECT/gke/cos_containerd/gke"`
shows both side by side.

Costs: the same VM minutes as a plan pass, plus the cluster management fee for the hours
the cluster exists (the free tier covers one zonal cluster) and a few minutes of cluster
creation and deletion.

## Scheduling fidelity: does the graph level change what Fluxion decides?

Design: [design/scheduling-fidelity.md](design/scheduling-fidelity.md). Entirely local; no
cloud resources, no API calls. The three graph levels describe the same region at
increasing detail, and this experiment asks what each level gets right for a scheduler
and what it costs Fluxion to hold and search.

```bash
make fidelity-graphs PROJECT=$PROJECT    # series, coarse and exact graphs from the captures, plus
                                         # exact at 1, 2, 4, 8 nodes per offer and a no-NIC ablation
make fidelity PROJECT=$PROJECT           # generate the workload, run every variant and policy, report
make fidelity VARIANTS="coarse exact" POLICIES="first locality"   # a subset
make fidelity-report PROJECT=$PROJECT    # rebuild fidelity.md from the sessions
```

`experiments/cmd/jobspecs` writes the workload: 33 single jobs in families (cores on one
socket and across two, memory, GPUs near cores, multi-node placement, NIC bandwidth, local
SSD, a pd-ssd pool) and a 200-job stream drawn from the families every level can express,
one YAML per job per level because each level needs the containment hierarchy spelled to
its own depth. `experiments/scripts/fidelity.sh` runs one `resource-query` session per
graph variant and match policy: the single jobs are matched and cancelled in turn, so each
sees an empty graph, then the stream is matched and kept, so the graph fills. Every session
records the load time, resident memory (`/usr/bin/time -v`), and per match the elapsed time
and Fluxion's preorder and postorder visit counts. `experiments/cmd/fidelity report` turns
the sessions into `fidelity.json` and `fidelity.md` under `projects/$PROJECT/fidelity/`: the
graphs' size and cost, the feasibility and placement matrix, agreement with the exact graph,
the match policies compared, the scale series, and where the policies disagree.

### Findings (2026-11-07, us-central1, 56 captured types; full report in `projects/llnl-flux/fidelity/fidelity.md`, and the Experiments page of the site)

The first run of this experiment found a defect in the graph builder and the numbers
below are from the second run, after the fix: a machine type whose series has no capture
at all was modelled as one socket with one core per vCPU, while captured types carry
hwloc cores (two vCPUs each on x86), so catalog-only types looked like huge single-socket
machines and took 24 of the 31 first-fit placements. Catalog-only cores are now vCPUs per
thread, the socket carries `threads_per_core`, the offer still says `topology=catalog`,
and `--captured-only` builds an exact graph without them (the `exact-captured` variant).
Of the 603 types in the region 56 are captured, 1642 offers are derived (a size cut from
a captured member of the same series) and 317 are catalog-only.

**What each level costs.** Memory and load time are linear in vertices, about 7 KB and
20 µs per vertex: the series graph (110k vertices) loads in 1.8 s and holds 0.7 GB, the
coarse graph (124k) 2.4 s and 0.8 GB, the quota-sized exact graph for the region (2.49M
vertices, a 1.3 GB JGF) 50 s and 18.3 GB, and the captured-only exact graph (2.31M) 48 s
and 17.6 GB. The devcontainer's 58 GB would hold about 8M vertices; a region at quota is
a third of that.

**What a match costs is the policy's doing, not the level's.** Fluxion's `first` and
`firstnodex` stop at the first fit: 50k visits (2% of the full exact graph) and 10 ms per
match. Every ranking policy (`low`, `high`, `lonode`, `hinode`, `lonodex`, `hinodex`,
`locality`) visits 1.54M vertices (62%) and takes 1.5 s per match, growing linearly from
about 60 ms at 118k vertices; `variation` takes 4.0 s. A failed match is the expensive
case for first-fit (64 ms on the full graph, 482 ms on the captured-only graph where more
requests fail) and the cheap one for the ranking policies. A region-wide exact graph is
interactive only with a first-fit policy; with a ranking policy it needs partitioning, by
zone or by offer.

**Feasibility.** The series and coarse graphs placed every single job. The exact graph
rejected three, each correctly: 360 cores on one socket and 360 across two (no socket in
the region has 180 physical cores; the coarse graph's c4d-standard-384 is 384 vCPUs on two
96-core sockets), and 8 cores with 3800 GB on one socket (m1-ultramem-160 spreads its
memory over four). The captured-only graph rejected three more: 176 cores on one socket,
2048 GB on one socket, and 8 GPUs on one socket. Those three are satisfiable only by
catalog-only types (c4-standard-288-metal, c4-highmem-288-metal, a3-edgegpu-8g) whose
single socket is a guess, so the full exact graph's yes for them is unverified. The exact
level also makes the jobspec spell the socket split, which the coarse level does not.

**Where placements land.** On the full exact graph under `first`, 7 of the 30 placements
are on captured types, 18 on derived ones and 5 on catalog-only ones; under `low` 6, 15
and 9. Derived types are grounded in a capture of their series, so 25 of 30 first-fit
answers rest on measured hardware, against 7 of 31 before the fix.

**Which machine type you get is the policy's choice.** Fluxion's ID-ordered policies
encode the alphabetical order of machine type names: `low` places a 1-core job on
a2-highgpu-1g, a 32-core job on a3-edgegpu-8g and a 2-node job on a2-highgpu-2g, GPU
machines, because "a2" and "a3" sort first; `high` and `locality` put nearly every
CPU job on z3-highmem-88-standardlssd. `first` spreads over 27 types and `variation` over
23, `low` and `high` over 11. Agreement with the exact graph on machine type is low for
the other levels: coarse chose the same type for 8 of 30 jobs under `first` and 11 under
`low`, series never (it answers with a series pool). The captured-only graph agrees with
the full one on 24 of 27 under `first` and on every zone. Between exact graphs of
different sizes the ranking policies are stable (21 to 24 of 26 to 29 same type) and
first-fit is not (2 of 26 to 29): first-fit placement changes whenever the graph is
rebuilt at another size.

**Packing.** Over the 200-job stream the quota-sized exact graph placed 190 under every
policy, the captured-only graph 184, the coarse graph 188 under `first` and 168 under
`low`, the series graph 172 and 168. The first failure is always the same job, 8 GPUs
with 32 cores on one socket: it is the first job in the stream and the captured-only
graph has no such socket, so it fails all 16 of them, while the other graphs run out of
the catalog-only TPU and edge types that answer it after a few. The exact graph at eight
potential nodes per offer placed all 200, more than at quota, because quota caps the GPU
offers below eight nodes. Multi-node jobs need the offer to hold the nodes: one node per
offer places none, two the 2-node job, eight the 8-node job, and the quota-sized graph
the 16-node job on n2d-standard-48.

**NICs are free.** Dropping the NIC vertices (2% of the small exact graph) changed one
placement in 25 under `first`, saved 4% of memory and nothing measurable in match time,
and only made the NIC jobs inexpressible. Keep them.

**What to change next.** Catalog-only types still have a guessed socket count; a
jobspec that must not land on one can exclude `topology=catalog`, or use the
captured-only graph. Fluxion's ID-ordered policies are not placement policies for a cloud
catalog, where IDs are alphabetical; assigning IDs at build time in cost or capacity order
would make `low` mean cheapest. For a region-wide exact graph the practical operating
point is first-fit, or one graph per zone.
