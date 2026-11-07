# latency

Compares provisioning time across databases, one per provisioning path: how long each
machine type took to come up as a VM against a GKE node pool, with the fixed costs a path
adds, such as creating the cluster.

```bash
go run ./experiments/cmd/latency --data vm=projects/my-project/agent/claude-opus-5-5/deterministic --data gke=projects/my-project/gke/cos_containerd/gke --phases projects/my-project/gke/cos_containerd/phases.tsv
go run ./experiments/cmd/latency --data ... --format json
```

Each capture's `provision_seconds` is the time from asking for the machine to a shell on
it. The GKE provider also records `timings` (node pool and pod seconds), which appear as a
mean breakdown. Fixed costs come from the phases file, whose names are `<label>/<phase>`.
