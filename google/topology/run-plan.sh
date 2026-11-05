#!/usr/bin/env bash
# Runs every capture in a plan file (from make plan-topology) through `make topology`,
# continuing past failures, and logs each outcome.
#
# Usage: topology/run-plan.sh PLAN_FILE [LOG_FILE]
#
# Each log line holds the time, machine type, outcome (ok, stockout, quota or error), seconds,
# pass number and, for errors, the capture's error message, tab-separated. Captures that
# stocked out in every zone are retried after RETRY_WAIT seconds (default 1800), in up to
# RETRY_PASSES more passes (default 2); quota failures are not retried. The run stops after
# STOP_AFTER_ERRORS consecutive errors that are not about capacity (default 5); an error
# repeating the previous one's message counts once, so a run of machine types failing the
# same way does not stop the plan. It refuses to start while a capture instance exists
# (ALLOW_EXISTING=1 overrides) and holds a lock next to the plan so two runs cannot overlap.
# DATA, PROJECT and TOPOLOGY_AGENT pass through to make.
set -uo pipefail

if [[ $# -lt 1 || $1 == -h || $1 == --help ]]; then
  sed -n '2,15p' "$0" >&2
  exit 1
fi
cd "$(dirname "$0")/.." || exit 1
plan=$1
log=${2:-${plan%.*}.log}
retry_wait=${RETRY_WAIT:-1800}
retry_passes=${RETRY_PASSES:-2}
stop_after=${STOP_AFTER_ERRORS:-5}
agent=${TOPOLOGY_AGENT:-topology-agent}

lock=${plan}.lock
if ! mkdir "$lock" 2> /dev/null; then
  echo "run-plan: another run holds ${lock}; remove it if that run is gone" >&2
  exit 1
fi
trap 'rmdir "$lock"' EXIT

if [[ ${ALLOW_EXISTING:-0} != 1 ]]; then
  existing=$($agent cleanup ${PROJECT:+--project "$PROJECT"} 2>&1)
  if [[ $? -ne 0 || $existing != "no capture instances running" ]]; then
    echo "run-plan: refusing to start; delete capture instances (make cleanup DELETE=1) or set ALLOW_EXISTING=1:" >&2
    echo "$existing" >&2
    exit 1
  fi
fi

lines=()
while IFS= read -r line; do
  [[ $line == "make topology "* ]] && lines+=("$line")
done < "$plan"
if [[ ${#lines[@]} -eq 0 ]]; then
  echo "run-plan: no 'make topology' lines in ${plan}" >&2
  exit 1
fi
mkdir -p "$(dirname "$log")"

declare -A final
status=""
message=""

# capture LINE PASS runs one plan line and sets status from the capture's status file.
capture() {
  local line=$1 pass=$2 mt start rc status_file
  mt=$(sed -n 's/.*MACHINE_TYPE=\([^ ]*\).*/\1/p' <<< "$line")
  status_file=$(mktemp -u)
  echo "=== ${mt} (pass ${pass})" >&2
  start=$(date +%s)
  output=$(mktemp)
  eval "$line STATUS_FILE=$status_file" < /dev/null 2>&1 | tee "$output" >&2
  rc=${PIPESTATUS[0]}
  if [[ -s $status_file ]]; then
    status=$(tr -d '[:space:]' < "$status_file")
  else
    status=error
  fi
  rm -f "$status_file"
  # The capture succeeded but a later step (rendering, the index) failed.
  [[ $rc -ne 0 && $status == ok ]] && status=error
  # The capture's result JSON carries the error; keep its first line for the log.
  message=""
  if [[ $status != ok ]]; then
    message=$(sed -n 's/^ *"error": "\(.*\)",\{0,1\}$/\1/p' "$output" | head -1 | sed 's/\\n/ /g; s/\\"/"/g; s/  */ /g' | cut -c1-240)
  fi
  rm -f "$output"
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$mt" "$status" "$(($(date +%s) - start))" "$pass" "$message" >> "$log"
  final[$mt]=$status
}

pending=("${lines[@]}")
pass=1
consecutive=0
last_message=""
stopped=0
while :; do
  retry=()
  for line in "${pending[@]}"; do
    capture "$line" "$pass"
    case $status in
      ok|quota) consecutive=0 ;;
      stockout) consecutive=0; retry+=("$line") ;;
      *) [[ -n $message && $message == "$last_message" ]] || consecutive=$((consecutive + 1)); last_message=$message ;;
    esac
    if [[ $consecutive -ge $stop_after ]]; then
      echo "run-plan: stopping after ${consecutive} consecutive errors" >&2
      stopped=1
      break
    fi
  done
  [[ $stopped -eq 1 || ${#retry[@]} -eq 0 || $pass -gt $retry_passes ]] && break
  echo "run-plan: ${#retry[@]} capture(s) stocked out; retrying in ${retry_wait}s" >&2
  sleep "$retry_wait"
  pending=("${retry[@]}")
  pass=$((pass + 1))
done

ok=0; failed=0
for mt in "${!final[@]}"; do
  if [[ ${final[$mt]} == ok ]]; then ok=$((ok + 1)); else failed=$((failed + 1)); fi
done
skipped=$((${#lines[@]} - ok - failed))
echo "run-plan: ${ok} ok, ${failed} failed, ${skipped} not attempted; log in ${log}" >&2
[[ $failed -eq 0 && $skipped -eq 0 ]]
