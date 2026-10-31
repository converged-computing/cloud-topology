# quota

Snapshots a project's Compute Engine quotas, then works out how many nodes of each machine type
the project can run and which topology captures are still missing.

```bash
go run ./quota fetch --project my-project --regions us-central1      # -> projects/my-project/quota.json
go run ./quota plan --quota projects/my-project/quota.json --region us-central1 --models on-demand,flex-start
go run ./quota plan --quota projects/my-project/quota.json --region us-central1 --format commands
```

`sizes` lists the machine types in a series, and how many of each, that satisfy a request for
total vCPUs, GPUs and memory within quota, fewest nodes first. It is the tool for the agent that
chooses concrete sizes after the hyperscheduler matches on totals:

```bash
go run ./quota sizes --quota projects/my-project/quota.json --region us-central1 \
  --series n2-standard --vcpus 16 --memory-gb 64
```

Options use one machine type each; mixed sizes are not generated.

`plan --format commands` prints one topology capture per series: the largest size the project
can run on-demand, since smaller sizes are derived from it. By default it:

- prefers a variant without Local SSD (`-lssd`) at the same size, since Local SSD adds cost but
  not CPU or GPU layout;
- skips bare-metal types (`-metal`, their own series, because a VM is not a cut of a bare-metal
  host) and shared-core types (such as `e2-micro`); `--metal` and `--shared-core` include them;
- captures on Spot when quota allows (`--on-demand` turns this off), and adds
  `--maintenance-policy=TERMINATE` for GPU machine types;
- orders captures by GPUs, then vCPUs, so the cheapest run first;
- skips series already captured at that size or larger.

`--verify` adds each series' smallest runnable size, to check the derivation against a real
capture. Both `plan` outputs end with a report: quotas with no limit, families whose quota rule was
inferred from the snapshot, families with no rule (left out), families that are not ordinary VMs
(Cloud TPU hosts and legacy shared-core, not planned), series with no on-demand quota and the
quota that blocks them, and machine types not captured. A CPU
family missing from Google's table is still planned when the snapshot holds a `CPUS_PER_VM_FAMILY`
quota for it; the rule is marked as inferred.

`fetch` reads three sources:

- Compute Engine regional quotas (`regions.get`): classic metrics such as `CPUS`, `N2_CPUS`,
  `NVIDIA_A100_GPUS` and the `PREEMPTIBLE_*` metrics, with usage.
- Compute Engine project quotas (`projects.get`): `CPUS_ALL_REGIONS` and `GPUS_ALL_REGIONS`.
- The Cloud Quotas API: per-family quotas, recorded as `CPUS_PER_VM_FAMILY` (dimension
  `vm_family`) and `GPU_FAMILY` (dimension `gpu_family`). These come with limits only, no usage,
  so node counts limited by them are upper bounds (shown as `<=N`).

Google's examples address the Cloud Quotas API by project number. If a project ID is refused,
pass the project number to `--project`.

Which quota a machine type consumes follows the CPU and GPU tables in
[Allocation quotas](https://docs.cloud.google.com/compute/resource-usage) (last updated
2026-10-26), encoded in `pkg/quota/rules.go`:

- E2 and N1 share `CPUS`; N2, N2D, C2, C2D, C3, M1–M3, T2D and T2A have their own `*_CPUS`;
  N4, C3D, C4, Z3, H3, H4D, M4, X4 and similar use `CPUS_PER_VM_FAMILY`.
- A2, A3, A4, G2 and G4 need GPU quota only. A3 Ultra (H200), A4 (B200) and A4X (GB200) have no
  standard quota, so they cannot run on-demand.
- Spot and flex-start VMs use preemptible quota when the project has some, and otherwise fall
  back to standard quota.
- A missing regional quota counts as zero; a missing all-regions quota is ignored.

Machine families not in the tables are reported as unknown and left out of graphs. Update
`rules.go` when Google adds families. Quota is permission, not capacity: a zone can still be out
of stock.
