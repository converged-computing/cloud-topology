# server

Serves the database over HTTP. `kill -HUP <pid>` reloads it, and the quota snapshot, from disk.

```bash
go run ./server --data data --addr :8080
go run ./server --data data --quota projects/my-project/quota.json   # size graphs by quota
```

| Endpoint | Returns |
| --- | --- |
| `GET /healthz` | `ok` |
| `GET /v1/index` | database summary |
| `GET /v1/machine-types?region=&zone=&family=&accelerator=&min_gpus=` | matching machine types |
| `GET /v1/machine-types/{name}` | one machine type |
| `GET /v1/machine-types/{name}/topology` | captured hwloc XML |
| `GET /v1/machine-types/{name}/capture` | capture record |
| `GET /v1/graph?level=series\|coarse\|exact&series=&region=&machine_types=&models=&nodes=` | JGF graph; with a quota snapshot, sized by quota and `nodes` caps each offer |
| `GET /v1/capacity?region=&models=` | nodes quota allows per machine type (needs `--quota`) |
| `GET /v1/sizes?region=&series=&vcpus=&gpus=&memory_gb=&max_nodes=&model=` | machine types and counts in a series that satisfy the totals, within quota when loaded |

`pkg/client` is a small Go client for these endpoints.
