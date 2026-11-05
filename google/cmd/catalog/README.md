# catalog

Exports Compute Engine machine types into `data/machine-types/` using the Compute Engine API
(`machineTypes.aggregatedList`) and rewrites `data/index.json`.

```bash
gcloud auth application-default login
go run ./cmd/catalog --project my-project --regions us-central1 --families a3,g2,c3
go run ./cmd/catalog --index-only          # rebuild index.json only
```

Each machine type becomes one file listing every zone (within the chosen regions) that offers
it. Disk types are exported the same way into `data/disk-types/` (`diskTypes.aggregatedList`):
name, size range and the zones offering each. Deprecated machine types are skipped unless `--include-deprecated` is set. Files for machine
types that disappear from the catalog are not deleted automatically.

Not yet exported: accelerators that attach to general-purpose machines (for example N1 with T4),
prices, and quota.
