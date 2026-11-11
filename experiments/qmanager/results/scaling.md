# Scaling

How each backend's cost moves as the graph, and the allocation table, grow. Same seven
families and nine sizes (248 offers in us-east-1) with 4, 16 and 64 nodes per offer; the
folded graph the database holds is 3,342 vertices in every case, because the nodes of an
offer are alike and fold into one row with a count. Measured 2026-11-09, one broker, one
thread for the database's queries. `make graphs bench compare GRAPHS=graphs-16 MAX_NODES=16 TAG=-16`
reproduces a row of the first tables; `make profile ALLOCATIONS=10000` a row of the last.

## The exact graph grows, the folded one does not

Stream phase, 200 jobs with allocations kept:

| nodes per offer | nodes | exact vertices | Fluxion median | Fluxion p95 | Fluxion max | Fluxion wall | Fluxion memory | database median | database p95 | database max | database wall | database memory |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 4 | 992 | 32,828 | 1.2 ms | 42 ms | 94 ms | 1.3 s | 245 MB | 22.7 ms | 33 ms | 121 ms | 5.1 s | 179 MB |
| 16 | 3,848 | 115,196 | 1.7 ms | 149 ms | 385 ms | 4.1 s | 704 MB | 22.1 ms | 31 ms | 126 ms | 5.2 s | 177 MB |
| 64 | 14,512 | 419,268 | 1.8 ms | 508 ms | 2,594 ms | 17.9 s | 2,501 MB | 20.7 ms | 24 ms | 106 ms | 4.5 s | 175 MB |

Single phase, 33 jobs each against an empty graph:

| nodes per offer | Fluxion median | Fluxion p95 | Fluxion wall | database median | database p95 | database wall |
|---|---|---|---|---|---|---|
| 4 | 1.3 ms | 28 ms | 0.2 s | 19.1 ms | 25 ms | 0.8 s |
| 16 | 1.9 ms | 99 ms | 0.5 s | 20.8 ms | 25 ms | 0.8 s |
| 64 | 3.3 ms | 324 ms | 1.3 s | 19.1 ms | 21 ms | 0.8 s |

Decisions did not change with size on either side: the same 31 of 33 and 200 of 200 outcomes
agree at every scale, and the same jobs differ in machine type.

## The database, query by query

`profile.py` runs what the service runs for one match, one query at a time, on a copy of a
database with synthetic allocations on record (one node each, random offers, random hours
around now). The benchmark graph (248 offers) and the full two-cloud database (7,615
offers, 100,656 folded vertices); cells are median / p95 in milliseconds:

| database | allocations | match | rank (Python) | free check | reserve | cancel lookup | release | earliest start | a match pays |
|---|---|---|---|---|---|---|---|---|---|
| benchmark, 248 offers | 0 | 9.1 / 13 | 0.1 / 1 | 5.2 / 6 | 1.6 / 2 | 1.0 / 1 | 1.0 / 1 | 6.3 / 8 | 16 ms |
| benchmark, 248 offers | 1,000 | 8.1 / 10 | 0.1 / 0 | 4.7 / 5 | 1.3 / 2 | 0.9 / 2 | 0.9 / 1 | 6.3 / 7 | 14 ms |
| benchmark, 248 offers | 10,000 | 9.5 / 15 | 0.1 / 1 | 5.8 / 6 | 6.0 / 2 | 2.9 / 2 | 1.3 / 1 | 14.4 / 16 | 21 ms |
| benchmark, 248 offers | 100,000 | 35.5 / 254 | 0.1 / 0 | 19.7 / 21 | none placed | none placed | none placed | 1266.2 / 1381 | 55 ms |
| full, 7,615 offers | 0 | 33.6 / 99 | 3.3 / 16 | 11.7 / 14 | 1.8 / 9 | 1.0 / 1 | 1.0 / 1 | 13.0 / 15 | 50 ms |
| full, 7,615 offers | 1,000 | 31.4 / 89 | 3.7 / 16 | 12.2 / 14 | 2.0 / 9 | 1.2 / 2 | 0.9 / 1 | 13.3 / 16 | 49 ms |
| full, 7,615 offers | 10,000 | 32.9 / 96 | 4.4 / 19 | 13.4 / 15 | 3.1 / 6 | 2.1 / 3 | 2.3 / 5 | 16.3 / 18 | 54 ms |

A match that places pays match, rank, free check and reserve. The earliest-start query runs
only when the best offers are short of nodes; with 100,000 allocations on 248 offers every
offer is full, so nothing places and that query runs every time.
