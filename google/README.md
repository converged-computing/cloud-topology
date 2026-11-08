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
#   add --captured-only to cmd/graph for an exact graph without catalog-only machine types
make sizes REGION=$REGION SERIES=n2-standard VCPUS=16  # concrete sizes for a total
make serve                                             # http://localhost:8080/v1/index
```

Plans capture on Spot where quota allows (`ON_DEMAND=1` for on-demand only). See
`topology/README.md` for how captures run, retry and render.

## Experiments

Everything that runs captures to answer a question lives under `experiments/`, apart from
the production code that builds and serves the database: the agent-versus-runner
experiment, the consistency study over days, and the GKE experiment. See
`experiments/README.md`; the targets (`make experiment`, `make consistency`, `make gke`,
and their reports) work from this directory too.

## AWS

`../aws/` is the EC2 side: the same database, quota snapshot and graphs built from the
EC2 API, in its own Go module that imports the provider-neutral packages here. `make
graphdb` at the repository root folds the AWS graphs and loads them with the Google ones
into one LadybugDB (`FOLDED=` on `make graphdb` here does the same by hand), and
`make graphdb-query Q=providers` asks the same pattern of both clouds.

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
make graphdb-query Q=providers        # 8-accelerator offers with an RDMA-class NIC, per cloud (needs the AWS graphs)
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
make site REGION=$REGION EXPERIMENTS=projects/$PROJECT/agent/test
make site REGION=$REGION STUDY=projects/$PROJECT/consistency
make site REGION=$REGION SITE_DATA="main=data agent=projects/$PROJECT/agent/claude-opus-5-5/agent"
make site-final REGION=$REGION                                # final experiment run + consistency study, plus ../aws when present
make site-serve                                               # http://localhost:8000
```

`site-final` publishes the deterministic arm of the final experiment as the database
`compute-engine`; the agent arm captured the same cloud again, so it is hidden from the
database lists and reached from the experiment page (`SITE_LABELS` and `SITE_HIDE` hold
these; `LABELS=old=new` and `HIDE=label` work on `make site` too) and adds the AWS database
(`../aws/data`) and its folded graphs (`make -C ../aws folded`) as `aws` when they exist;
`FOLDED` passes folded graph files to `make site` directly. The published site is built
by `make site` at the repository root, which refolds the AWS graphs first and writes
`docs/`.

## Updating

Rerun `make catalog` to refresh machine types, and `make topology` to add or replace a topology.
Both rewrite `index.json`; `make index` rebuilds it alone. A running server reloads after
`kill -HUP <pid>`.

## Kubernetes Comparison

Make sure you have kubectl installed. 
```bash
sudo gcloud components install kubectl

# Test with three
make gke REGION=$REGION LIMIT=3
cat projects/$PROJECT/gke/consistency.md projects/$PROJECT/gke/latency.md

make gke REGION=$REGION                               # all 57 types on COS nodes
make gke REGION=$REGION IMAGE_TYPE=UBUNTU_CONTAINERD GKE=projects/$PROJECT/gke-ubuntu   # the other node image
make gke-report                                       # rebuild the reports
make site SITE_DATA="vm=projects/$PROJECT/agent/claude-opus-5-5/deterministic gke=projects/$PROJECT/gke/gke"
```

## License

MIT. See LICENSE, COPYRIGHT and NOTICE.
