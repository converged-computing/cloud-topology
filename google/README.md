# Google Cloud Topology

Derive graphs for Google Cloud instances our (or more generally, an account) has access to.

## Usage

Open the folder in a devcontainer, which provides Go, hwloc and gcloud. Then:

```bash
make test              # unit tests
make build             # the commands, into bin/
make examples          # regenerate example graphs from testdata/
```

The Fluxion examples need `resource-query` and Fluxion's Go bindings, which the devcontainer
builds from flux-sched into `/opt/flux-sched` (see `.devcontainer/claude/Dockerfile` to do the
same by hand). `make fluxion-go` sets the cgo flags for that checkout (`FLUX_SCHED` overrides
the path) unless `CGO_CFLAGS` and `CGO_LDFLAGS` are already in the environment.

```bash
make run-examples      # match the example graphs with resource-query
make fluxion-go        # match them with Fluxion's Go bindings
```

To build the real dataset, driven by what your project's quota allows:

```bash
gcloud auth login --no-launch-browser --update-adc

export PROJECT=my-project
export REGION=us-central1
gcloud auth application-default set-quota-project $PROJECT
gcloud config set project $PROJECT

# public machine type catalog
make catalog REGIONS=$REGION

# snapshot this project's quotas
make quota REGIONS=$REGION

# what the project can run, and how many
make plan REGION=$REGION

# one per series: the largest size quota allows, as make topology lines
make plan-topology REGION=$REGION

# captures need topology-agent (the devcontainer installs it; this reinstalls)
make install-agent

# run them one by one (each creates and deletes a VM), or all at once
make plan-topology REGION=$REGION | bash
make capture-all REGION=$REGION

# or hand the plan to the agent (log in once with `claude`, then /login)
make capture-agent REGION=$REGION

make graphs REGION=$REGION                             # series, coarse and exact graphs sized by quota
make sizes REGION=$REGION SERIES=n2-standard VCPUS=16  # concrete sizes for a total
make serve                                             # http://localhost:8080/v1/index
```

Plans capture on Spot where quota allows (`ON_DEMAND=1` for on-demand only). See
`topology/README.md` for how captures run, retry and render.

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
`projects/$PROJECT/experiment-test/`, with stockouts retried after 5 minutes instead of 30,
to check logins, quota and the agent before committing to the full plan. `make report
EXPERIMENT=projects/$PROJECT/experiment-test` reads its result.

Everything lands in `projects/$PROJECT/experiment/`:

| File | What it is |
| --- | --- |
| `captures.txt` | the plan both arms ran (`make plan-topology RECAPTURE=1`); kept across reruns, delete it for a new plan |
| `agent/` | the agent arm's database: `topology/<type>/{capture.json,hwloc.xml,topology.svg}` and `index.json`, with `machine-types` linked from `data/` |
| `agent-result.json`, `agent.log` | the agent's messages and log |
| `deterministic/` | the runner's database, laid out the same way |
| `deterministic-captures.log` | one line per attempt: time, machine type, `ok`/`stockout`/`quota`/`error`, seconds, pass, error message |
| `phases.tsv` | each arm's start, end, seconds and exit status |
| `report.md`, `report.json` | the comparison: per-arm captured counts and rates, wall clock, mean provisioning time, what either arm got, and a per-machine-type table |

Each arm creates one VM per plan line, a few minutes each when it succeeds, so a 25-line plan
takes one to two hours per arm plus the runner's retry waits. Run it detached and follow the
output:

```bash
mkdir -p projects/$PROJECT/experiment
nohup make experiment REGION=$REGION > projects/$PROJECT/experiment/run.out 2>&1 &
tail -f projects/$PROJECT/experiment/run.out
```

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
(`make graphs DATA=projects/$PROJECT/experiment/agent GRAPHS=projects/$PROJECT/experiment/agent`) or copy `topology/<type>` directories
into `data/` and run `make index`.

## Consistency study

Is a machine type's hardware the same every time? `make consistency` captures the whole
plan repeatedly, in rounds spread over days, each round into its own database under
`projects/$PROJECT/consistency/rounds/<timestamp>/`, with the zone order rotated by one
place each round so a type is seen in different zones. The deterministic runner does the
capturing; the study is about the hardware, not the strategy.

```bash
export PROJECT=my-project REGION=us-central1
nohup make consistency REGION=$REGION > projects/$PROJECT/consistency/run.out 2>&1 &   # 6 rounds, 12 hours apart
make consistency REGION=$REGION ROUNDS=4 INTERVAL=21600                                 # 4 rounds, 6 hours apart
make consistency-report EXTRA_DATA="main=data agent=projects/$PROJECT/experiment/agent"  # include other databases
```

A round takes as long as `make capture-all` (about two hours for 57 types, plus retry
waits) and costs the same; the full study is one VM per type per round. A restarted study
continues from the rounds already on disk. After every round the report is rebuilt in
`consistency.md` and `consistency.json`: for each machine type, how many captures and how
many distinct shapes, which fields vary (sockets, cores, PUs, NUMA nodes, memory, L3 and L2
sizes, CPU model, GPU and NIC placement), and for varying types every capture with its
zone, model, time, kernel and hwloc version. `make site STUDY=projects/$PROJECT/consistency`
adds the rounds to the web interface, whose Consistency view shows the same.

## Graph queries

The resource graphs can be queried with Cypher in an embedded graph database,
[LadybugDB](https://github.com/LadybugDB/ladybug). The pieces:

- The graph for us-central1 has 2,652,544 vertices and 2,652,543 edges modeling every core and adding NICs with egress bandwidth and kind (gVNIC, TCPX, RDMA) and storage for disk types that can attach depending on the machine type.
- I don't store the expanded graph, and especially because we are modeling resources that we will create, I fold it up. To do this I start with the fluxion JGF and walk from the bottom up and give each vertex a signature based on hash of its type, properties, and signatures of children with their counts (and I leave out the names). Siblings with the same signature can then merge onto each other with a count. This reduces the 2 million vertices to 22K, and this is what I'm calling motifs in the updated UI.
- I found an embedded Cypher database https://github.com/LadybugDB/ladybug that I can export into from Parquet! Loading takes under a second and queries under a third of s second. See the screenshot for an example.

To build it and ask questions:

```bash
make install-lbug                     # the LadybugDB shell, once (the devcontainer has it)
make graphdb REGION=$REGION           # fold the final run's graph and data/, export, load graphdb/graph.lbug

make graphdb-query Q=summary          # rows and folded siblings per graph and vertex type
make graphdb-query Q=shapes           # node shapes by how many machine types, zones and regions carry them
make graphdb-query Q=pattern          # offers that fit 8 GPUs, a GPU or RDMA NIC and a Hyperdisk ML pool
make graphdb-query Q=varying          # machine types whose node shape differs between graphs
make graphdb-query MODE=csv Q=shapes  # csv, tsv, json, jsonlines, markdown or box output

# your own Cypher, inline or from a file
make graphdb-query Q="MATCH (o:Vertex {type:'offer'})-[:Contains]->(p:Vertex {type:'pd_ssd'}) RETURN o.zone, count(*)"
go run ./cmd/graphdb query --file my.cypher
lbug graphdb/graph.lbug               # the interactive shell
```

Here is an example. Whose nodes have at least 8 GPUs and a GPU NIC, and which also have a Hyperdisk ML pool?

```cypher
MATCH (o:Vertex {type: 'offer'})-[:Contains]->(p:Vertex {type: 'placement'})-[:Contains]->(n:Vertex {type: 'node'})
MATCH (n)-[:Contains]->(nic:Vertex {type: 'nic'})
WHERE o.gpus >= 8 AND nic.kind IN ['rdma', 'tcpx', 'tcpxo']
MATCH (o)-[:Contains]->(pool:Vertex {type: 'hyperdisk_ml'})
RETURN o.zone, o.machine_type, o.egress_gbps, p.count * n.count AS nodes, pool.size AS hyperdisk_ml_gb;
```

## Web interface

```bash
make site REGION=$REGION                                      # data/ as "main", into site/
make site REGION=$REGION EXPERIMENTS=projects/$PROJECT/experiment-test
make site REGION=$REGION STUDY=projects/$PROJECT/consistency
make site REGION=$REGION SITE_DATA="main=data agent=projects/$PROJECT/experiment/agent"
make site-final REGION=$REGION                                # final experiment run + consistency study only
make site-serve                                               # http://localhost:8000
```

## Updating

Rerun `make catalog` to refresh machine types, and `make topology` to add or replace a topology.
Both rewrite `index.json`; `make index` rebuilds it alone. A running server reloads after
`kill -HUP <pid>`.

## License

MIT. See LICENSE, COPYRIGHT and NOTICE.
