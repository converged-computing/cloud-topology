#!/usr/bin/env bash
# The full ingest experiment, unattended: every request through the deterministic engine
# and the agent on Bedrock with Claude Opus 5, three repeats, strict then flexible, then
# the report. Runs in the background and returns at once; the log says where it is.
#
#   experiments/ingest/batch.sh            start it (or resume it: finished runs are skipped)
#   experiments/ingest/batch.sh status     what is done, what is running, the report if finished
#   experiments/ingest/batch.sh stop       stop a running batch
#   experiments/ingest/batch.sh archive [label]   move results/ to archive/<date-time>/ (a fresh
#                                          start); with a label, the site shows it beside the latest
#   experiments/ingest/batch.sh clean      delete results/ (a fresh start, nothing kept)
#
# Environment overrides: MODEL (us.anthropic.claude-opus-5, the cross-region inference
# profile; the bare model id is denied in this account), BACKEND (aws), REPEAT (3),
# TIMEOUT (600 seconds per agent run), ARMS ("strict flexible").
# Nothing here touches EC2 or any AWS service but the Bedrock runtime.
set -uo pipefail
cd "$(dirname "$0")"
export BACKEND=${BACKEND:-aws} MODEL=${MODEL:-us.anthropic.claude-opus-5} REPEAT=${REPEAT:-3} TIMEOUT=${TIMEOUT:-600}
arms=${ARMS:-strict flexible}
log=results/batch.log
pidfile=results/batch.pid

running() { [[ -f $pidfile ]] && kill -0 "$(cat "$pidfile")" 2> /dev/null; }

case ${1:-start} in
  status)
    if running; then echo "batch running (pid $(cat "$pidfile")); last lines of ${log}:"; else echo "batch not running; last lines of ${log}:"; fi
    tail -n 8 "$log" 2> /dev/null
    echo
    echo "finished agent runs: $(find "results/${BACKEND}" -path "*/${MODEL//\//_}*/*.agent.out" -size +0 2> /dev/null | xargs -r grep -l '"usage"' 2> /dev/null | wc -l) of $(( $(grep -c '^- name:' requests.yaml) * REPEAT * $(wc -w <<< "$arms") )) for ${BACKEND} ${MODEL}"
    [[ -f results/report.md ]] && { echo; cat results/report.md; }
    exit 0 ;;
  stop)
    # The batch runs in its own process group, so this stops run.sh and the agent too.
    if running; then kill -TERM -- "-$(cat "$pidfile")"; echo "stopped"; else echo "nothing running"; fi
    exit 0 ;;
  archive|clean)
    if running; then echo "a batch is running; $0 stop first" >&2; exit 1; fi
    [[ -d results ]] || { echo "no results to ${1}"; exit 0; }
    if [[ $1 == archive ]]; then
      dest=archive/$(date -u +%Y%m%dT%H%M%SZ)
      mkdir -p archive && mv results "$dest" && echo "moved results/ to ${dest}/"
      # A label puts the batch on the site's page next to the latest one: archive "what changed".
      if [[ -n ${2:-} ]]; then echo "$2" > "$dest/LABEL"; echo "labelled: $2"; fi
    else
      rm -rf results && echo "deleted results/"
    fi
    exit 0 ;;
  start) ;;
  _run)
    echo "batch: started $(date -u +%FT%TZ) backend=${BACKEND} model=${MODEL} repeat=${REPEAT} arms=${arms}"
    for arm in $arms; do
      echo "batch: arm ${arm}"
      if [[ $arm == flexible ]]; then FLEXIBLE=1 ./run.sh results; else ./run.sh results; fi
      [[ $? == 3 ]] && { echo "batch: stopped, the backend refused; fix access and start again"; exit 1; }
    done
    echo "batch: finished $(date -u +%FT%TZ); report in results/report.md"
    exit 0 ;;
  *) echo "usage: $0 [start|status|stop|archive|clean]" >&2; exit 2 ;;
esac

if running; then
  echo "already running (pid $(cat "$pidfile")); $0 status to watch, $0 stop to stop" >&2
  exit 1
fi
command -v ingest-agent > /dev/null || { echo "ingest-agent is not installed (pip install -e ../../ingest-agent[aws])" >&2; exit 1; }
[[ -f ../../google/graphdb/graph.lbug ]] || { echo "no graph database at ../../google/graphdb/graph.lbug (make graphdb at the root)" >&2; exit 1; }

mkdir -p results
# setsid: the batch leads its own process group (its pid is the group id), which stop kills whole.
setsid "$0" _run > "$log" 2>&1 < /dev/null &
echo $! > "$pidfile"
echo "batch started (pid $(cat "$pidfile")); log: ${log}; $0 status to check, $0 stop to stop"
