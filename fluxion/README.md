# fluxion-graphdb

Fluxion's resource service, answered from the graph database.

Fluxion is two Flux modules. `sched-fluxion-qmanager` holds the queues and runs the
policies (fcfs, easy, conservative, hybrid). `sched-fluxion-resource` holds the resource
graph and answers one question over Flux RPCs: can this jobspec run now, and if not, when.
The queue manager never touches the graph, so the resource module can be replaced. This
package registers the same service name and speaks the same protocol, and answers from
the folded graphs of every cloud and the `Allocation` table in LadybugDB instead of a
graph in memory. `make graphdb` at the top of this repository builds that database;
[ingest-agent](https://github.com/converged-computing/ingest-agent) is the matcher and the
allocation ledger it answers with.

```bash
pip install -e ingest-agent -e fluxion      # into the interpreter flux python uses
flux start -s1
flux module remove sched-simple
flux python -m fluxion_graphdb --db google/graphdb/graph.lbug --prices aws=aws/projects/<account>/prices.json &
flux module load sched-fluxion-qmanager queue-policy=easy
flux submit -N2 -n2 --requires=... hostname
```

A job's R names the offer and the node count, not hosts: `scheduling.offer` is the offer's
path (`/site0/aws/us-east-1/us-east-1a/c7i.48xlarge.on-demand`), with the provider, zone,
machine type, model, node count and price beside it, and `execution.R_lite` carries the
ranks, cores and GPUs Flux expects. The decision is where and when; the site holding that
offer's exact graph places and runs the job.

## What it answers

| RPC | Answer |
|---|---|
| `match` (`allocate`, `allocate_orelse_reserve`, `allocate_with_satisfiability`, `satisfiability`) | `ALLOCATED` with R and `at` now; `RESERVED` with the earliest start at which the nodes are free for the duration; `MATCHED` for satisfiability alone; `EBUSY` when nothing is free now (and reserving was not asked); `ENODEV` when no offer in any cloud can ever hold the request |
| `match_multi` | the same, streamed per job, stopping at the first failure as Fluxion does |
| `cancel`, `partial-cancel` | release the job's allocation |
| `update` | take a running job's R as given (qmanager replays them on restart) |
| `info`, `stats-get`, `stats-clear`, `status`, `params`, `notify` | what qmanager reads of them |

Allocations are written as request `flux:<jobid>`, so a restarted service picks the jobs
up again. Candidates are ranked by fit (the least of the node left idle) or by price with
`--rank price`, and the first one with the nodes free wins; the database holds whole
nodes, since in a cloud a node is a VM that is paid for whole.

The database is held open through LadybugDB's Python package for the service's lifetime;
`ingest-agent allocations --db ...` reads beside it. `--driver shell` runs every statement
through the `lbug` shell instead, at about half a second a match.

[experiments/qmanager](../experiments/qmanager) benchmarks this against flux-sched's own
resource module on the same graph and runs qmanager over the database end to end.

## Layout

| Path | What it is |
| --- | --- |
| `fluxion_graphdb/resource.py` | the decisions: match, cancel, update, info, stats, with Fluxion's errnos; no Flux in it |
| `fluxion_graphdb/service.py` | the Flux service: the RPC topics onto those decisions |
| `fluxion_graphdb/cli.py` | `fluxion-graphdb`, or `flux python -m fluxion_graphdb` |

`pytest` runs the tests against a faked database.
