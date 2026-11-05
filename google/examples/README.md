# examples

Graphs in `graphs/` are generated from `testdata/` (`make examples`), so they describe the
synthetic `test-*` machine types, not real ones.

- `coarse-us-central1.json` with `jobspecs/coarse-2gpu-nodes.yaml`: three 2-GPU nodes from one
  offer. The offer has four nodes, so the first match succeeds and the next two fail.
- `exact-us-central1.json` with `jobspecs/exact-gpu-near-cores.yaml`: two nodes in one zone, each
  with a GPU and four cores on the same socket. There are eight GPU sockets across two zones, so
  four requests succeed and the fifth fails.

The series example uses `testdata/quota-example`, sized by its synthetic quota snapshot:

- `series-us-central1.json` with `jobspecs/series-16-cores.yaml`: 16 vCPUs and 64 GB in total,
  any machine size. The first match takes 16 of the 20 N2 vCPUs quota allows. The second has
  only 4 N2 vCPUs left, so Fluxion places it on the G2 pool, whose GPU machines also have
  vCPUs; preferring CPU series for CPU-only work is for the layer above Fluxion.
- `quota sizes --series n2-standard --vcpus 16` then lists the concrete choices (2 x
  n2-standard-8, 4 x n2-standard-4, 8 x n2-standard-2). `exact-n2-standard-8.json` with
  `jobspecs/exact-8-cores.yaml` is the exact graph for the first choice.

Two ways to run them, both inside the devcontainer:

```bash
./run.sh                       # resource-query
cd fluxion-go && go run . -graph ../graphs/exact-us-central1.json \
  -jobspec ../jobspecs/exact-gpu-near-cores.yaml -count 5      # Fluxion Go bindings
```

`fluxion-go` is its own Go module because it uses cgo; its `go.mod` points at the flux-sched
checkout in `/opt/flux-sched`, and the devcontainer sets `CGO_CFLAGS` and `CGO_LDFLAGS`. Add
`-reserve` to see future reservations instead of failures.
