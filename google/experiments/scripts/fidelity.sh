#!/usr/bin/env bash
# Scheduling fidelity: runs the workload through Fluxion's resource-query for every graph
# variant and match policy, entirely locally. Nothing here touches a cloud or an API.
#
# Usage: experiments/scripts/fidelity.sh GRAPHS_DIR JOBSPECS_DIR OUT_DIR
#
# GRAPHS_DIR holds <variant>.json graphs whose name starts with the level (series, coarse,
# exact, exact-n4, exact-n1-no-nic); JOBSPECS_DIR is from experiments/cmd/jobspecs. One
# resource-query session per variant and policy: the single jobs are matched and cancelled
# in turn (each sees an empty graph), then the stream is matched and kept (packing).
#
# Environment: POLICIES (default every resource-query policy), VARIANTS (default every
# graph), RESOURCE_QUERY (the binary). A finished session is not rerun.
set -uo pipefail

if [[ $# -lt 3 || $1 == -h || $1 == --help ]]; then
  sed -n '2,13p' "$0" >&2
  exit 1
fi
graphs=$1
jobspecs=$2
out=$3
rq=${RESOURCE_QUERY:-/opt/flux-sched/build/resource/utilities/resource-query}
policies=${POLICIES:-first low high lonode hinode lonodex hinodex firstnodex locality variation}
variants=${VARIANTS:-}
jobs=$jobspecs/jobs.tsv
[[ -x $rq ]] || { echo "fidelity: no resource-query at ${rq} (set RESOURCE_QUERY)" >&2; exit 1; }
[[ -f $jobs ]] || { echo "fidelity: no ${jobs}; run experiments/cmd/jobspecs first" >&2; exit 1; }
if ! command -v /usr/bin/time > /dev/null; then
  echo "fidelity: /usr/bin/time is required for memory measurements" >&2
  exit 1
fi

for graph in "$graphs"/*.json; do
  variant=$(basename "$graph" .json)
  [[ -z $variants || " $variants " == *" $variant "* ]] || continue
  level=${variant%%-*}
  mkdir -p "$out/$variant"
  for policy in $policies; do
    in=$out/$variant/$policy.in
    log=$out/$variant/$policy.log
    if [[ -f $log.rc ]]; then
      echo "fidelity: ${variant} ${policy} done; skipping" >&2
      continue
    fi
    # Single jobs are cancelled by the job id resource-query hands out, which advances on
    # every match, failed ones included.
    # An ablation graph lacks a vertex type; a jobspec naming it is rejected by Fluxion
    # without advancing the job id, which would throw the cancels off, so such jobs are
    # left out of that variant (n/a in the report).
    nonic=0; [[ $variant == *no-nic* ]] && nonic=1
    awk -F'\t' -v level="$level" -v dir="$jobspecs/$level" -v nonic="$nonic" '
      NR > 1 && index($NF, level) && !(nonic && $9 > 0) { n++; printf "match allocate %s/%s.yaml\n", dir, $1; if ($2 == "single") printf "cancel %d\n", n }
      END { print "quit" }' "$jobs" > "$in"
    echo "fidelity: ${variant} with policy ${policy} ($(grep -c '^match' "$in") matches)" >&2
    /usr/bin/time -v -o "$log.time" "$rq" -L "$graph" -f jgf -F simple -P "$policy" -e -d < "$in" > "$log" 2>&1
    echo $? > "$log.rc"
    grep -E "Graph Load Time|Maximum resident" "$log" "$log.time" | sed 's/^/  /' >&2
  done
done
