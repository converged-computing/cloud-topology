#!/usr/bin/env bash
# One benchmark run inside a Flux instance: load a resource backend, drive the workload
# through it, record the answers.
#
#   flux start -s1 bash run.sh fluxion single runs/fluxion-single.json
#   flux start -s1 bash run.sh graphdb single runs/graphdb-single.json
#
# fluxion loads flux-sched's own sched-fluxion-resource module with graphs/exact.json;
# graphdb starts fluxion-graphdb (this repository's fluxion/) over graphs/db/graph.lbug
# (its allocations are cleared first). CMD, LIMIT and JOBSPECS pass through to driver.py; DRIVER=shell
# makes the database service use the lbug shell instead of the ladybug package; GRAPHS names
# the directory holding exact.json and db/.
set -euo pipefail
ulimit -c 0  # flux-sched's resource module aborts at broker exit; no core files
backend=$1 phase=$2 out=$3
here=$(cd "$(dirname "$0")" && pwd)
so=${FLUXION_SO:-/opt/flux-sched/build/resource/modules/sched-fluxion-resource.so}
ingest=${INGEST_AGENT_SRC:-$here/../../ingest-agent}
fluxion=${FLUXION_GRAPHDB_SRC:-$here/../../fluxion}
graphs=${GRAPHS:-$here/graphs}
db=${DB:-$graphs/db/graph.lbug}
drive() {
  flux python "$here/driver.py" --backend "$backend" --phase "$phase" --out "$out" --jobspecs "${JOBSPECS:-$here/jobspecs}" \
    ${CMD:+--cmd "$CMD"} ${LIMIT:+--limit "$LIMIT"} --rss-pid "$1"
}
flux module remove -f sched-simple 2>/dev/null || true
case $backend in
  fluxion)
    flux module load "$so" load-file="$graphs/exact.json" load-format=jgf match-format="${MATCH_FORMAT:-jgf}" policy="${POLICY:-first}" \
      prune-filters=ALL:core,ALL:node,ALL:gpu
    drive "$(flux getattr broker.pid)"
    flux module remove sched-fluxion-resource
    ;;
  graphdb)
    lbug "$db" --mode csv --no_progress_bar --no_stats <<<"MATCH (a:Allocation) DELETE a;" >/dev/null
    PYTHONPATH="$fluxion:$ingest${PYTHONPATH:+:$PYTHONPATH}" flux python -m fluxion_graphdb --db "$db" ${DRIVER:+--driver "$DRIVER"} ${PRICES:+--prices "$PRICES"} &
    pid=$!
    trap 'kill $pid 2>/dev/null || true' EXIT
    drive "$pid"
    ;;
  *) echo "backend must be fluxion or graphdb" >&2; exit 2 ;;
esac
