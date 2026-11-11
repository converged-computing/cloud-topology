#!/usr/bin/env bash
# Fluxion's queue manager scheduling real Flux jobs over the graph database. Inside a
# Flux instance: start fluxion-graphdb's resource service, load sched-fluxion-qmanager in
# place of sched-simple, submit the single-phase jobspecs with test execution (they run
# for three seconds without hosts), wait, and show what qmanager decided.
#
#   flux start -s1 bash qmanager.sh
set -euo pipefail
ulimit -c 0  # flux-sched's resource module aborts at broker exit; no core files
here=$(cd "$(dirname "$0")" && pwd)
so=${QMANAGER_SO:-/opt/flux-sched/build/qmanager/modules/sched-fluxion-qmanager.so}
ingest=${INGEST_AGENT_SRC:-$here/../../ingest-agent}
fluxion=${FLUXION_GRAPHDB_SRC:-$here/../../fluxion}
db=${DB:-$here/graphs/db/graph.lbug}
policy=${QUEUE_POLICY:-easy}
limit=${LIMIT:-12}

lbug "$db" --mode csv --no_progress_bar --no_stats <<<"MATCH (a:Allocation) DELETE a;" >/dev/null
flux module remove -f sched-simple 2>/dev/null || true
PYTHONPATH="$fluxion:$ingest${PYTHONPATH:+:$PYTHONPATH}" flux python -m fluxion_graphdb --db "$db" &
pid=$!
trap 'kill $pid 2>/dev/null || true' EXIT
for _ in $(seq 50); do flux python -c "import flux; flux.Flux().rpc('sched-fluxion-resource.params').get()" 2>/dev/null && break; sleep 0.2; done
flux module load "$so" queue-policy="$policy"
flux module list | grep -i fluxion

tmp=$(mktemp -d)
ids=()
while IFS=$'\t' read -r id phase _; do
  [[ $phase == single ]] || continue
  (( ${#ids[@]} < limit )) || break
  flux python - "$here/jobspecs/exact/$id.yaml" "$tmp/$id.json" <<'PY'
import json, sys, yaml
js = yaml.safe_load(open(sys.argv[1]))
# Flux's job ingest takes jobspec version 1, rooted at the node; the database service reads
# the same request from either form.
node = js["resources"]
while node and node[0]["type"] != "node":
    node = node[0]["with"]
js["version"], js["resources"] = 1, node
js.setdefault("attributes", {}).setdefault("system", {})["exec"] = {"test": {"run_duration": "3s"}}
js["attributes"]["system"]["job"] = {"name": sys.argv[1].rsplit("/", 1)[-1][:-5]}
json.dump(js, open(sys.argv[2], "w"))
PY
  ids+=("$(flux job submit "$tmp/$id.json")")
done < <(tail -n +2 "$here/jobspecs/jobs.tsv")
echo "submitted ${#ids[@]} jobs under queue policy $policy"
sleep 1
echo; echo "== while running"
flux jobs -a -o "{id.f58:>12} {name:<14} {status:<10} {nnodes:>3} {nodelist}"
flux python -c "import flux, json; print(json.dumps(flux.Flux().rpc('sched-fluxion-resource.stats-get').get()))"
PYTHONPATH="$ingest" python3 -m ingest_agent allocations --db "$db" | cut -c1-140
for id in "${ids[@]}"; do flux job wait-event -t 30 "$id" clean >/dev/null || true; done
sleep 2  # the free of the last job reaches the service a moment after its clean event
echo; echo "== done"
flux jobs -a -o "{id.f58:>12} {name:<14} {status:<10} {nnodes:>3} {runtime:>6.1f}s  {nodelist}"
echo; echo "== one job's R"
flux job info "${ids[0]}" R | python3 -c "import json,sys; R=json.load(sys.stdin); print(json.dumps(R['scheduling'], indent=1)); print(R['execution']['nodelist'])"
echo; echo "== allocations left on record (should be none)"
PYTHONPATH="$ingest" python3 -m ingest_agent allocations --db "$db"
flux module remove sched-fluxion-qmanager
