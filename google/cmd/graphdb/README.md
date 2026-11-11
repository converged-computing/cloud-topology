# graphdb

Puts the folded resource graphs into a graph database for Cypher queries. The fold is what
makes this practical: identical subtrees share a signature, so the exact graph of a region,
millions of vertices expanded, is tens of thousands of rows, and a motif query is a pattern
over them.

```bash
make install-lbug                          # the LadybugDB shell, once
make graphdb REGION=us-central1            # export Parquet tables, load graphdb/graph.lbug
make graphdb-query Q=shapes                # node shapes and how widely each occurs
make graphdb-query Q=pattern               # offers that can hold a GPU + RDMA + Hyperdisk ML pattern
make graphdb-query Q=varying               # machine types with more than one node shape
make graphdb-query Q="MATCH (v:Vertex {type:'offer'}) RETURN v.zone, count(*)"
lbug graphdb/graph.lbug                    # interactive shell
```

The shipped queries live in `pkg/site/queries/` (the site's Motifs page shows the same
ones); `query --named shapes` runs one, `--file` a file of your own, or pass the Cypher as
arguments.

`export` builds and folds the graphs (`--data label=dir`, repeatable, with `--quota` and
`--region`) and writes three tables: `vertices.parquet`, one row per folded entry with its
count, size, signature and the properties queries want as columns (machine type, vCPUs,
GPUs, egress, NIC kind, disk type); `edges.parquet`, containment edges carrying the count;
and `shapes.parquet`, one row per distinct subtree with a description such as
`2 socket, 96 core, 8 gpu, 1 nic, 680 GB memory` and how often it occurs. `load` creates the
schema (`Vertex` and `Shape` node tables, `Contains` and `HasShape` relationships) and
copies the tables in. `query` runs a Cypher file or string through the shell.

The tables are plain Parquet, so they load into DuckDB, Neo4j or any other engine as well;
the graphs in the database are a derived artifact, rebuilt by `make graphdb`, never the
source of truth. Several providers go in the same database with different `provider` and
`graph` values.

The schema also has an `Allocation` table, which is not derived: it is the scheduler's
record of nodes of an offer held for a span of time (`offer` is the offer vertex's path,
`nodes`, `from_time`, `to_time`, `request`). ingest-agent's `match` subtracts it from the
quota counts to say what is free over a request's window and when the rest comes free, and
its `reserve` and `release` write it. `load` saves the table to `allocations.parquet`
before it recreates the database and copies it back in, so a rebuild keeps them.
