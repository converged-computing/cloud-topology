#!/usr/bin/env bash
# The ingest experiment: every request in requests.yaml through the deterministic engine
# (the baseline) and through the agent, with one backend and model; results land under
# OUT/<backend>/<model>/<request>.{engine.json,agent.out,agent.err}. A finished request is
# not rerun. Nothing here touches a cloud; the agent's model calls are the only cost.
#
# Usage: experiments/run.sh [OUT]
# Environment: DB (the LadybugDB database), PRICES (provider=path, repeatable with
# spaces), BACKEND (claude, default; aws for Bedrock), MODEL (claude-opus-5-5 by default;
# for aws a Bedrock model id such as anthropic.claude-opus-5-5), REQUESTS (names to run,
# default all), MAX_MATCHES (agent budget, default 6), REPEAT (runs per request, default 1),
# FLEXIBLE=1 (the agent may reshape a request within reason; off by default), TIMEOUT
# (seconds per agent run, default 600). Runs are headless: the agent's interactive mode is
# for ingest-agent run by hand.
set -uo pipefail
out=$(realpath -m "${1:-$(dirname "$0")/results}")
cd "$(dirname "$0")"
db=${DB:-../../google/graphdb/graph.lbug}
prices=${PRICES:-}
backend=${BACKEND:-claude}
model=${MODEL:-claude-opus-5-5}
requests=${REQUESTS:-}
max_matches=${MAX_MATCHES:-6}
repeat=${REPEAT:-1}
flexible=${FLEXIBLE:-0}
# Each agent run is cut off after TIMEOUT seconds (default 600): a run that loops cannot
# spend more than that, and the batch moves on.
run_timeout=${TIMEOUT:-600}
[[ -f $db ]] || { echo "run: no database at ${db} (set DB; cloud-topology's make graphdb builds it)" >&2; exit 1; }
if [[ -z $prices ]]; then
  for p in ../../aws/projects/*/prices.json; do [[ -f $p ]] && prices="$prices aws=$p"; done
  for p in ../../google/projects/*/prices.json; do [[ -f $p ]] && prices="$prices google-cloud=$p"; done
fi
price_args=(); for p in $prices; do price_args+=(--prices "$p"); done
dir=$out/$backend/${model//\//_}${FLEXIBLE:+-flexible}
mkdir -p "$dir"
# One run per results directory: two would write the same files at once.
exec 9> "$out/.lock"
flock -n 9 || { echo "run: another run is using ${out}; stop it first (batch.sh stop)" >&2; exit 1; }

python3 - "$requests" <<'PY' > "$dir/.requests.tsv"
import sys, yaml
only = set(sys.argv[1].split()) if sys.argv[1].strip() else None
for r in yaml.safe_load(open("requests.yaml")):
    if only and r["name"] not in only:
        continue
    # "-" stands for an empty field, since read collapses consecutive tabs.
    print(r["name"], r.get("floor") or "-", r.get("expect") or "-", " ".join(r["text"].split()), sep="\t")
PY

while IFS=$'\t' read -r name floor expect text; do
  [[ $floor == - ]] && floor=""
  for ((i = 1; i <= repeat; i++)); do
    tag=$name; [[ $repeat -gt 1 ]] && tag="$name.$i"
    if [[ -n $floor && ! -f $dir/$name.engine.json ]]; then
      ingest-agent match --request "$floor" --db "$db" "${price_args[@]}" --format json --top 10 > "$dir/$name.engine.json" 2> /dev/null
    fi
    if [[ -s $dir/$tag.agent.out ]] && grep -q '"usage"' "$dir/$tag.agent.out"; then
      echo "run: ${tag} done; skipping" >&2
      continue
    fi
    echo "=== ${tag} (${backend} ${model})" >&2
    args=(--text "$text" --db "$db" "${price_args[@]}" --backend "$backend" --model "$model" --max-matches "$max_matches" --auto-approve)
    [[ $flexible == 1 ]] && args+=(--flexible)
    [[ -n $floor ]] && args+=(--request "$floor")
    start=$(date +%s)
    timeout "$run_timeout" ingest-agent run "${args[@]}" > "$dir/$tag.agent.out" 2> "$dir/$tag.agent.err"
    rc=$?
    echo "rc=${rc} seconds=$(( $(date +%s) - start ))" >> "$dir/$tag.agent.err"
    # A backend that refuses (no access, no such model, bad credentials) will refuse every
    # run the same way: stop the batch and say so instead of recording forty failures.
    if [[ $rc -ne 0 ]] && grep -qE "AccessDenied|not authorized|ResourceNotFound|could not be found|ValidationException|UnrecognizedClient|ExpiredToken|InvalidSignature|NoCredentials|authentication" "$dir/$tag.agent.err"; then
      echo "run: ${tag} failed before the agent could work; the backend refused:" >&2
      grep -E "Exception|Error|not authorized" "$dir/$tag.agent.err" | head -2 | cut -c1-400 >&2
      echo "run: stopping the batch; fix the backend and run again (finished runs are kept)" >&2
      exit 3  # batch.sh stops on 3
    fi
  done
done < "$dir/.requests.tsv"
python3 report.py "$out" --site "$out/ingest.json" > "$out/report.md" && echo "run: report in ${out}/report.md" >&2
