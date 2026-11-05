# consistency

Compares every capture of each machine type across databases and reports which machine
types captured the same hardware every time. It reads the rounds of a consistency study
(`topology/consistency.sh`, run by `make consistency`) and any other databases you name.

```bash
go run ./cmd/consistency --study projects/my-project/consistency
go run ./cmd/consistency --study projects/my-project/consistency --data main=data --data agent=projects/my-project/experiment/agent
go run ./cmd/consistency --study ... --format json
```

Each capture gets a fingerprint: sockets, cores, PUs, NUMA nodes, memory, L3 and L2 sizes,
CPU models, and where GPUs and NICs attach (which socket, or the machine). Captures of a
type with different fingerprints are listed with the fields that differ, their zone,
provisioning model and time. The kernel and hwloc version of each capture are recorded but
not part of the fingerprint, so the report can tell hardware variance from a changed
capture environment.
