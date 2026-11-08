#!/usr/bin/env bash
# Runs every EC2 capture in a plan file (from make plan-topology) through `make topology`,
# one instance at a time, continuing past failures, and logs each outcome.
#
# Usage: capture/run-plan.sh PLAN_FILE [LOG_FILE]
#
# Guardrails: it refuses to start while any tagged capture instance exists in the region
# (preflight), runs one capture at a time (each delete waits for EC2 to report the instance
# terminated), and ends by listing tagged instances and volumes again (postflight), exiting
# non-zero with the terminate command if anything is left. Every instance also terminates
# itself after its time limit, whatever happens here. DRY_RUN=1 prints the plan's lines
# and estimate and launches nothing.
#
# Each log line holds the time, machine type, outcome (ok, stockout, quota or error), seconds,
# pass number and, for errors, the capture's error message, tab-separated. Captures that
# stocked out in every zone are retried after RETRY_WAIT seconds (default 1800), in up to
# RETRY_PASSES more passes (default 2); errors are retried in those passes too, since most
# are transient (an AMI lookup, a Spot request that EC2 cancels), while quota failures are
# not. A failure never stops the run: the type joins the retry set and the plan moves on.
# STOP_AFTER_ERRORS=N (default 0, off) stops after N consecutive errors that are not about
# capacity, for runs where a broken runner should not touch every type; an error repeating
# the previous one's message counts once. SKIP_CAPTURED=1 skips lines whose machine type is
# already captured in DATA, so a rerun finishes what an interrupted run left. ALLOW_EXISTING=1
# overrides the preflight; a lock next to the plan keeps two runs from overlapping.
# DATA, REGION and TOPOLOGY_AGENT pass through to make.
set -uo pipefail

if [[ $# -lt 1 || $1 == -h || $1 == --help ]]; then
  sed -n '2,24p' "$0" >&2
  exit 1
fi
cd "$(dirname "$0")/.." || exit 1
plan=$1
log=${2:-${plan%.*}.log}
retry_wait=${RETRY_WAIT:-1800}
retry_passes=${RETRY_PASSES:-2}
stop_after=${STOP_AFTER_ERRORS:-0}
agent=${TOPOLOGY_AGENT:-topology-agent}
region=${REGION:-us-east-1}

# The plan's header holds the estimate; show it before anything runs.
grep '^#' "$plan" | sed 's/^# \{0,1\}/run-plan: /' >&2
if [[ ${DRY_RUN:-0} == 1 ]]; then
  echo "run-plan: DRY_RUN=1, launching nothing; the plan's lines:" >&2
  grep '^make topology' "$plan" >&2
  exit 0
fi

lock=${plan}.lock
if ! mkdir "$lock" 2> /dev/null; then
  echo "run-plan: another run holds ${lock}; remove it if that run is gone" >&2
  exit 1
fi
trap 'rmdir "$lock"' EXIT

if [[ ${ALLOW_EXISTING:-0} != 1 ]]; then
  existing=$($agent cleanup --provider aws --region "$region" 2>&1)
  if [[ $? -ne 0 || $existing != "no capture instances running" ]]; then
    echo "run-plan: refusing to start; terminate capture instances (make cleanup DELETE=1) or set ALLOW_EXISTING=1:" >&2
    echo "$existing" >&2
    exit 1
  fi
fi

# postflight lists what carries our tag after the run; anything left is a failure.
postflight() {
  local left volumes
  left=$($agent cleanup --provider aws --region "$region" 2>&1)
  volumes=$(aws ec2 describe-volumes --region "$region" --filters Name=tag:purpose,Values=topology-capture Name=status,Values=available,in-use --query 'Volumes[].[VolumeId,State,Size]' --output text 2>&1)
  if [[ $left != "no capture instances running" || -n $volumes ]]; then
    echo "run-plan: POSTFLIGHT FAILED: capture resources still exist in ${region}:" >&2
    echo "$left" >&2
    [[ -n $volumes ]] && echo "volumes: $volumes" >&2
    echo "run-plan: terminating them now with: $agent cleanup --provider aws --region $region --delete" >&2
    $agent cleanup --provider aws --region "$region" --delete >&2
    echo "run-plan: check with: aws ec2 describe-instances --region $region --filters Name=tag:purpose,Values=topology-capture --query 'Reservations[].Instances[].[InstanceId,State.Name,InstanceType]' --output table" >&2
    return 1
  fi
  echo "run-plan: postflight clean: no capture instances or volumes in ${region}" >&2
}


lines=()
skipped_captured=0
while IFS= read -r line; do
  [[ $line == "make topology "* ]] || continue
  if [[ ${SKIP_CAPTURED:-0} == 1 ]]; then
    mt=$(sed -n 's/.*MACHINE_TYPE=\([^ ]*\).*/\1/p' <<< "$line")
    if [[ -f ${DATA:-data}/topology/$mt/capture.json ]]; then
      skipped_captured=$((skipped_captured + 1))
      continue
    fi
  fi
  lines+=("$line")
done < "$plan"
[[ $skipped_captured -gt 0 ]] && echo "run-plan: skipping ${skipped_captured} machine type(s) already captured in ${DATA:-data}" >&2
if [[ ${#lines[@]} -eq 0 ]]; then
  if [[ $skipped_captured -gt 0 ]]; then
    echo "run-plan: nothing left to capture" >&2
    exit 0
  fi
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
  # Plan lines end in a price comment; drop it so the variables appended here are seen.
  line=${line%%#*}
  status_file=$(mktemp -u)
  echo "=== ${mt} (pass ${pass})" >&2
  start=$(date +%s)
  output=$(mktemp)
  eval "$line STATUS_FILE=$status_file" < /dev/null 2>&1 | tee "$output" >&2
  rc=${PIPESTATUS[0]}
  if [[ -s $status_file ]]; then
    status=$(tr -d '[:space:]' < "$status_file")
  elif [[ $rc -eq 0 && -f ${DATA:-data}/topology/$mt/capture.json && $(stat -c %Y "${DATA:-data}/topology/$mt/capture.json") -ge $start ]]; then
    # No status file but the capture landed and make succeeded: the capture is good.
    status=ok
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
      *) [[ -n $message && $message == "$last_message" ]] || consecutive=$((consecutive + 1)); last_message=$message; retry+=("$line") ;;
    esac
    if [[ $stop_after -gt 0 && $consecutive -ge $stop_after ]]; then
      echo "run-plan: stopping after ${consecutive} consecutive errors" >&2
      stopped=1
      break
    fi
  done
  [[ $stopped -eq 1 || ${#retry[@]} -eq 0 || $pass -gt $retry_passes ]] && break
  echo "run-plan: ${#retry[@]} capture(s) stocked out or failed; retrying in ${retry_wait}s" >&2
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
postflight || exit 1
[[ $failed -eq 0 && $skipped -eq 0 ]]
