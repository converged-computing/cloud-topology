#!/usr/bin/env bash
# Captures the same plan repeatedly, in rounds spread over days, to see whether a machine
# type's hardware is consistent: across hosts, zones and time. Each round is a database of
# its own under OUT/rounds/<timestamp>/ (machine types linked from DATA), captured by
# run-plan.sh with the plan's zone lists rotated by one position per round, so a type is
# seen in different zones over the study. The report (experiments/cmd/consistency) lands in
# OUT/consistency.md and OUT/consistency.json after every round.
#
# Usage: experiments/scripts/consistency.sh PLAN_FILE OUT_DIR
#
# Environment: ROUNDS (default 6), INTERVAL seconds between round starts (default 43200,
# twice a day), and run-plan.sh's RETRY_WAIT, RETRY_PASSES and STOP_AFTER_ERRORS, DATA,
# PROJECT and TOPOLOGY_AGENT. Rounds already in OUT/rounds count towards ROUNDS, so a
# restarted study continues where it stopped. EXTRA_DATA lists other databases (label=dir)
# to include in the report, for example the experiment arms.
set -uo pipefail

if [[ $# -lt 2 || $1 == -h || $1 == --help ]]; then
  sed -n '2,16p' "$0" >&2
  exit 1
fi
cd "$(dirname "$0")/../.." || exit 1
plan=$1
out=$2
data=${DATA:-data}
rounds=${ROUNDS:-6}
interval=${INTERVAL:-43200}
[[ -f $plan ]] || { echo "consistency: no plan file ${plan}" >&2; exit 1; }
mkdir -p "$out/rounds"

# rotate N reads the plan and writes it with every ZONE list rotated left by N places.
rotate() {
  awk -v n="$1" '
    /^make topology / {
      if (match($0, /ZONE=[^ ]+/)) {
        zones = substr($0, RSTART + 5, RLENGTH - 5); k = split(zones, z, ",")
        r = n % k; outz = ""
        for (i = 0; i < k; i++) { j = ((i + r) % k) + 1; outz = outz (i ? "," : "") z[j] }
        $0 = substr($0, 1, RSTART - 1) "ZONE=" outz substr($0, RSTART + RLENGTH)
      }
    }
    { print }' "$plan"
}

report() {
  local args=()
  for d in ${EXTRA_DATA:-}; do args+=(--data "$d"); done
  go run ./experiments/cmd/consistency --study "$out" "${args[@]}" --format json > "$out/consistency.json" &&
    go run ./experiments/cmd/consistency --study "$out" "${args[@]}" > "$out/consistency.md"
}

done_rounds=$(find "$out/rounds" -mindepth 1 -maxdepth 1 -type d | wc -l)
while [[ $done_rounds -lt $rounds ]]; do
  id=$(date -u +%Y%m%dT%H%M%SZ)
  dir=$out/rounds/$id
  mkdir -p "$dir/topology"
  for sub in machine-types disk-types; do
    if [[ ! -e $dir/$sub && -d $data/$sub ]]; then
      ln -s "$(realpath --relative-to="$dir" "$data/$sub")" "$dir/$sub"
    fi
  done
  rotate "$done_rounds" > "$dir/captures.txt"
  echo "consistency: round $((done_rounds + 1)) of ${rounds} -> ${dir} (zones rotated by ${done_rounds})" >&2
  start=$(date +%s)
  DATA=$dir bash topology/run-plan.sh "$dir/captures.txt" "$dir/captures.log"
  rc=$?
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "$(date -u -d "@$start" +%Y-%m-%dT%H:%M:%SZ)" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(( $(date +%s) - start ))" "$done_rounds" "$rc" >> "$out/rounds.tsv"
  DATA=$dir bash topology/render.sh
  go run ./cmd/catalog --data "$dir" --index-only
  report
  echo "consistency: round $((done_rounds + 1)) finished with exit ${rc}; $(grep -c . "$out/consistency.md") report lines in ${out}/consistency.md" >&2
  done_rounds=$((done_rounds + 1))
  if [[ $done_rounds -lt $rounds ]]; then
    wait_for=$(( interval - ($(date +%s) - start) ))
    if [[ $wait_for -gt 0 ]]; then
      echo "consistency: next round at $(date -u -d "@$(( $(date +%s) + wait_for ))" +%Y-%m-%dT%H:%M:%SZ)" >&2
      sleep "$wait_for"
    fi
  fi
done
echo "consistency: ${done_rounds} rounds done; report in ${out}/consistency.md" >&2
