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
