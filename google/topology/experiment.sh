#!/usr/bin/env bash
# Runs one capture plan twice from the same starting state, to compare an agent working the
# plan with a deterministic runner: first topology-agent's agent captures into OUT/agent,
# then run-plan.sh captures into OUT/deterministic. Each arm is a database of its own, with
# the machine types linked from DATA, so `make graphs DATA=OUT/agent` works afterwards. The
# run ends with the comparison (cmd/report) in OUT/report.md and OUT/report.json.
#
# Usage: topology/experiment.sh PLAN_FILE OUT_DIR
#
# Environment: PROJECT, DATA (default data), BACKEND (default claude), MODEL, MAX_ATTEMPTS and
# TOPOLOGY_AGENT as for make capture-agent; RETRY_WAIT, RETRY_PASSES and STOP_AFTER_ERRORS as
# for run-plan.sh. Both arms get the same patience: RETRY_PASSES retries per stocked-out
# machine type, each after a wait of up to RETRY_WAIT seconds. ARMS picks the arms to run (default "agent deterministic"); an arm that
# already finished is skipped unless FORCE=1, which starts it over without its captures and
# log. Capture instances left running after an arm are deleted before the next one
# (KEEP_LEFTOVERS=1 keeps them, and the next arm then refuses to start).
set -uo pipefail

if [[ $# -lt 2 || $1 == -h || $1 == --help ]]; then
  sed -n '2,16p' "$0" >&2
  exit 1
fi
cd "$(dirname "$0")/.." || exit 1
plan=$1
out=$2
data=${DATA:-data}
agent=${TOPOLOGY_AGENT:-topology-agent}
backend=${BACKEND:-claude}
arms=${ARMS:-agent deterministic}
phases=$out/phases.tsv

[[ -f $plan ]] || { echo "experiment: no plan file ${plan}" >&2; exit 1; }
mkdir -p "$out"

# prepare ARM makes OUT/ARM a database that shares DATA's machine types.
prepare() {
  mkdir -p "$out/$1/topology"
  for sub in machine-types disk-types; do
    if [[ ! -e $out/$1/$sub && -d $data/$sub ]]; then
      ln -s "$(realpath --relative-to="$out/$1" "$data/$sub")" "$out/$1/$sub"
    fi
  done
}

# leftovers deletes capture instances still running, unless KEEP_LEFTOVERS=1.
leftovers() {
  local listing
  listing=$($agent cleanup ${PROJECT:+--project "$PROJECT"} 2>&1) || true
  [[ $listing == "no capture instances running" ]] && return 0
  echo "experiment: capture instances left running after the ${1} arm:" >&2
  echo "$listing" >&2
  if [[ ${KEEP_LEFTOVERS:-0} != 1 ]]; then
    $agent cleanup ${PROJECT:+--project "$PROJECT"} --delete >&2
  fi
}

# record ARM START EXIT appends one line to phases.tsv.
record() {
  local now
  now=$(date +%s)
  printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$(date -u -d "@$2" +%Y-%m-%dT%H:%M:%SZ)" "$(date -u -d "@$now" +%Y-%m-%dT%H:%M:%SZ)" "$((now - $2))" "$3" >> "$phases"
}

run_agent() {
  echo "experiment: agent arm (${backend}${MODEL:+ $MODEL}) -> ${out}/agent; messages in ${out}/agent-result.json, log in ${out}/agent.log" >&2
  $agent run --plan "$plan" --out "$out/agent" --backend "$backend" ${MODEL:+--model "$MODEL"} --auto-approve \
    ${PROJECT:+--project "$PROJECT"} ${MAX_ATTEMPTS:+--max-attempts "$MAX_ATTEMPTS"} \
    --retry-passes "${RETRY_PASSES:-2}" --retry-wait "${RETRY_WAIT:-1800}" \
    > "$out/agent-result.json" 2> >(tee "$out/agent.log" >&2)
}

run_deterministic() {
  echo "experiment: deterministic arm -> ${out}/deterministic; log in ${out}/deterministic-captures.log" >&2
  DATA=$out/deterministic bash topology/run-plan.sh "$plan" "$out/deterministic-captures.log"
}

for arm in $arms; do
  case $arm in
    agent|deterministic) ;;
    *) echo "experiment: unknown arm ${arm}" >&2; exit 1 ;;
  esac
  if [[ -f $out/$arm.done && ${FORCE:-0} != 1 ]]; then
    echo "experiment: ${arm} arm already finished (${out}/${arm}.done); FORCE=1 reruns it" >&2
    continue
  fi
  if [[ -f $out/$arm.done || -d $out/$arm/topology ]]; then
    echo "experiment: starting the ${arm} arm over" >&2
    rm -rf "$out/$arm.done" "$out/$arm/topology" "$out/$arm/index.json" "$out/$arm-captures.log" "$out/$arm-result.json" "$out/$arm.log"
    sed -i "/^${arm}\t/d" "$phases" 2> /dev/null
  fi
  prepare "$arm"
  start=$(date +%s)
  run_$arm
  rc=$?
  record "$arm" "$start" "$rc"
  DATA=$out/$arm bash topology/render.sh
  go run ./cmd/catalog --data "$out/$arm" --index-only
  touch "$out/$arm.done"
  echo "experiment: ${arm} arm finished with exit ${rc} after $(( $(date +%s) - start ))s" >&2
  leftovers "$arm"
done

go run ./cmd/report --plan "$plan" --dir "$out" --format json > "$out/report.json" &&
  go run ./cmd/report --plan "$plan" --dir "$out" | tee "$out/report.md"
