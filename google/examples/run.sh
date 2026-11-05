#!/usr/bin/env bash
# Matches each example jobspec against its graph with resource-query, three times,
# so the output shows allocation and then exhaustion.
set -euo pipefail
cd "$(dirname "$0")"
rq=${RESOURCE_QUERY:-resource-query}

run() {
  local graph=$1 jobspec=$2
  echo "=== graphs/${graph}.json + jobspecs/${jobspec}.yaml"
  printf 'match allocate jobspecs/%s.yaml\n' "$jobspec" "$jobspec" "$jobspec" | sed '$a quit' |
    "$rq" -L "graphs/${graph}.json" -f jgf -F simple -P first |
    grep -E "JOBID|RESOURCES|No matching|\.on-demand\[|us-central1-[a-z]\["
}

run coarse-us-central1 coarse-2gpu-nodes
run exact-us-central1 exact-gpu-near-cores
run series-us-central1 series-16-cores
run exact-n2-standard-8 exact-8-cores
run exact-n2-standard-8 exact-8-cores-ssd
