# Fluxion's queue manager over the graph database

Fluxion is two Flux modules. `sched-fluxion-qmanager` holds the queues and runs the
policies (fcfs, easy, conservative, hybrid). `sched-fluxion-resource` holds the resource
graph and answers one question over Flux RPCs: can this jobspec run now, and if not, when.
The queue manager never touches the graph. That makes the resource module replaceable,
and [fluxion-graphdb](../../fluxion) replaces it. It registers the same service name,
speaks the same protocol (`match`, `match_multi`, `cancel`, `update`, `info`, `notify`,
`status`), and answers from the folded graphs and the `Allocation` table in LadybugDB
instead of a graph in memory, through ingest-agent's matcher.

This directory tests Fluxion's own resource module against that stand-in on the same
graph, and runs Fluxion's queue manager over the database with real submitted jobs.

## The two backends

| | Fluxion | graph database |
|---|---|---|
| holds | the exact JGF in memory, every core a vertex | the folded graph in LadybugDB, one row per distinct subtree with a count |
| allocation unit | any vertex, so cores on a shared node | whole nodes of an offer |
| over time | a planner per vertex, in memory, gone on restart | the `Allocation` table, on disk, kept across rebuilds |
| process | a broker module | a Python service under `flux python`, holding the database open through LadybugDB's package |
| picks | by match policy (`first` here, the first vertex that fits) | tightest fit, price breaking ties |
| answers | ALLOCATED, RESERVED, EBUSY, ENODEV | the same |

Both see the same graph. `make graphs` builds an exact JGF for a subset of AWS instance
types (seven families, nine sizes, four nodes per offer, 992 nodes and 32,828 vertices in
us-east-1) and, from the same build, the folded version that `make db` loads into a
database. The subset is there because Fluxion must hold the exact graph in memory; the
full region's exact graph is 518 MB on disk for AWS and 1.7 GB for Google.

## The benchmark

`make jobspecs` writes the scheduling fidelity experiment's workload, 33 single jobs and
a stream of 200, as exact-level jobspecs that spell out Fluxion's hierarchy. `make bench`
sends them through [driver.py](driver.py) to whichever backend is loaded, with the same
`match` RPC qmanager uses (`allocate_orelse_reserve`), in two phases. Single: match and
cancel each job, so each sees an empty graph. Stream: match every job and keep the
allocations, so later jobs see what earlier ones took and some get reserved for later.
`make compare` writes the tables to `results/`.

## Results

Measured on 2026-11-09 in the devcontainer, flux-sched v0.56.0 and LadybugDB 0.22.1, one
broker, nothing else running. `results/compare.md` has the full tables with every job
where the machine types differ.

**Single phase, 33 jobs, each against an empty graph**

| | Fluxion | graph database |
|---|---|---|
| allocated | 22 | 22 |
| unsatisfiable | 10, plus 1 EINVAL | 11 |
| median latency | 1.3 ms | 19 ms |
| p95 latency | 28 ms | 32 ms |
| memory | 245 MB | 178 MB |

Same outcome on 31 of 33 jobs. Same machine type on 3 of the 21 both placed.

**Stream phase, 200 jobs, allocations kept**

| | Fluxion | graph database |
|---|---|---|
| allocated | 154 | 179 |
| reserved for later | 25 | 0 |
| unsatisfiable | 21 | 21 |
| median latency | 1.2 ms | 23 ms |
| p95 latency | 45 ms | 33 ms |
| wall for the stream | 1.3 s | 5.1 s |

Same outcome on 200 of 200 jobs. Same machine type on 25 of 179. Fluxion reserved 25
GPU jobs for an hour later because its `first` policy had filled the g6.48xlarge nodes it
kept choosing; the database spread the GPU jobs over g6 sizes by fit and never ran out.

**qmanager over the database** (`make qmanager`, easy policy, 12 jobs): every satisfiable
job was scheduled, ran its three seconds and completed, each on the machine type the database
chose; the three impossible ones (176 and 360 cores) were rejected as unsatisfiable, and
the allocation table was empty when the last job finished.

## What the differences mean

- **Fit.** Fluxion's `first` policy takes the first vertex that fits, so a 4-core job lands
  on an m7i.8xlarge and shares it. The database picks the smallest node that holds the
  job, because in a cloud a node is a VM that is paid for whole. Fluxion's other policies
  (`low`, `lonode`) would come closer; the comparison here is of mechanism, not policy.
- **Sockets.** The database tallies cores, GPUs and memory per node and ignores which socket
  they are on, so a request for 8 GPUs across 2 sockets is placed on a g6.48xlarge whose
  GPUs hang off the node. Fluxion refuses it, because its graph has no GPU under a socket.
  Fluxion is right about the shape and the database is right about the machine.
- **Accelerator machines.** The database keeps CPU work off GPU machines, so a request for
  8 cores and a local SSD finds nothing in this subset, where only g6 has local SSDs.
  Fluxion places it on a g6.
- **Unknown resource types.** A pd-ssd pool, which AWS does not have, is EINVAL from
  Fluxion and ENODEV from the database. Both mean no.
- **Speed and memory.** Fluxion answers from memory in about a millisecond and holds 245 MB
  for this graph. The database answers in about 20 ms, three or four queries of a few
  milliseconds each through LadybugDB's Python package on one thread, and the service
  holds 178 MB with the database open. Its slowest answers are faster than Fluxion's:
  Fluxion's p95 is a multi-node or GPU match walking the exact graph, the database's is
  a query a little larger than the rest. Through the `lbug` shell instead (`DRIVER=shell`)
  a match took half a second, almost all of it starting the shell, and the service held
  29 MB.

## Scaling

[results/scaling.md](results/scaling.md) has the tables. Two things move.

- **The exact graph grows; the folded one does not.** With 16 and then 64 nodes per offer
  (115,196 and 419,268 vertices) Fluxion's median stays near 2 ms, but its p95 goes from
  42 ms to 149 ms to 508 ms, its worst match to 2.6 s, its memory from 245 MB to 704 MB to
  2.5 GB, and the 200-job stream from 1.3 s to 17.9 s. The database does not change: the
  folded graph is 3,342 vertices at every size, because the nodes of an offer are alike
  and fold into one row with a count, so the stream stays near 5 s and 175 MB and the p95
  under 35 ms. The crossover for the whole stream is between 16 and 64 nodes per offer.
  With every node a vertex, Fluxion's matching walks and prunes thousands of them for a
  job the database settles by arithmetic on a count.
- **Allocations.** The free and earliest-start queries join the `Allocation` table to each
  candidate offer. Up to about 40 allocations per offer (10,000 on 248 offers) they cost a
  few milliseconds more. At 400 per offer the earliest-start query takes 1.3 s and the
  match query's p95 reaches 254 ms: both unwind every allocation start inside the window
  against every allocation covering it, so an offer's cost grows with the square, and for
  the earliest start the cube, of its allocations. The fix is to fetch an offer's
  allocations once and sweep them in Python, which is linear; the match query would then
  carry only the quota count. Not done yet. On the full database with 10,000 allocations
  spread over 7,615 offers nothing moves.

Query by query on the full two-cloud database: the match query is 34 ms at the median and
99 ms at p95, growing with the offers that pass the filter (a plain 8-core request returns
5,000 rows, which Python then merges and ranks in 3 to 16 ms); the free check and the
earliest start are 12 to 13 ms each, almost all of it finding the offer rows by path, a
scan of 100,656 vertices because `path` is not the primary key; writes and lookups are 1
to 3 ms. A placing match pays about 50 ms there against 15 ms on the benchmark graph.

## qmanager over the database

`make qmanager` starts fluxion-graphdb, loads `sched-fluxion-qmanager` with a queue policy
in place of `sched-simple`, submits the single jobs with Flux's test execution (they run
for three seconds without hosts) and shows what the queue manager decided: each job's machine
type and zone from the R the database returned, and the allocation table emptying as jobs
finish. That is Fluxion's queue, policies and job lifecycle, with no resource graph in
memory.

## Layout

- [run.sh](run.sh), one backend and phase inside a Flux instance
- [driver.py](driver.py), the workload through the `match` RPC, answers to JSON
- [compare.py](compare.py), two runs side by side
- [qmanager.sh](qmanager.sh), the queue manager end to end
- [profile.py](profile.py), the database's queries one at a time, with allocations on record
- `graphs*/`, `jobspecs/`, `runs/`, generated and ignored; `results/`, the tables, kept
