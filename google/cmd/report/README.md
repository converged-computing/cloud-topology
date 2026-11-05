# report

Compares the arms of a capture experiment run by `topology/experiment.sh`: the same plan
worked by topology-agent's agent and by the deterministic runner, each into its own database.

```bash
go run ./cmd/report --plan projects/my-project/experiment/captures.txt --dir projects/my-project/experiment
go run ./cmd/report --plan ... --dir ... --format json
```

For each arm it reports how many planned machine types were captured (a `capture.json` in
the arm's database), the arm's wall-clock time and exit status from `phases.tsv`, and the
mean provisioning time of its captures. For the deterministic arm the outcome of each machine
type comes from `run-plan.sh`'s log (`ok`, `stockout`, `quota` or `error`, with attempts and
time); the agent leaves no per-type record of failures, so its misses are `not captured`.
`either` counts machine types that at least one arm captured, and the report ends with
captures found outside the plan.
