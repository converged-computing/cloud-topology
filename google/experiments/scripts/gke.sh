#!/usr/bin/env bash
# Captures a plan on one-node node pools of a GKE cluster, to check that a Kubernetes node
# sees the same hardware as a VM and to measure what the Kubernetes path costs in time.
# Creates the cluster (timed), runs the plan through topology/run-plan.sh with the gke
# provider into OUT/gke/, deletes the cluster (also on error or Ctrl-C, after removing any
# capture node pool left behind), and writes the reports: consistency.md
# (shapes against the VM captures in COMPARE) and latency.md (provisioning time per type,
# with the cluster creation as a fixed cost).
#
# Usage: experiments/scripts/gke.sh PLAN_FILE OUT_DIR
#
# Environment: PROJECT, DATA (default data), TOPOLOGY_AGENT, CLUSTER (default topo),
# IMAGE_TYPE (COS_CONTAINERD or UBUNTU_CONTAINERD), KEEP_CLUSTER=1 to leave the cluster,
# COMPARE (label=dir databases to compare with, default the final experiment's
# deterministic arm when present), and run-plan.sh's RETRY_WAIT, RETRY_PASSES and
# STOP_AFTER_ERRORS. A rerun captures only what is missing (SKIP_CAPTURED=0 recaptures all).
set -uo pipefail

if [[ $# -lt 2 || $1 == -h || $1 == --help ]]; then
  sed -n '2,16p' "$0" >&2
  exit 1
fi
cd "$(dirname "$0")/../.." || exit 1
plan=$1
out=$2
data=${DATA:-data}
cluster=${CLUSTER:-topo}
image=${IMAGE_TYPE:-COS_CONTAINERD}
phases=$out/phases.tsv
[[ -f $plan ]] || { echo "gke: no plan file ${plan}" >&2; exit 1; }
[[ -n ${PROJECT:-} ]] || { echo "gke: PROJECT is required" >&2; exit 1; }
zone=$(sed -n 's/^make topology .*ZONE=\([^, ]*\).*/\1/p' "$plan" | head -1)
[[ -n $zone ]] || { echo "gke: no capture lines in ${plan}" >&2; exit 1; }
mkdir -p "$out/gke/topology"
for sub in machine-types disk-types; do
  if [[ ! -e $out/gke/$sub && -d $data/$sub ]]; then
    ln -s "$(realpath --relative-to="$out/gke" "$data/$sub")" "$out/gke/$sub"
  fi
done

record() { # NAME START EXIT
  local now; now=$(date +%s)
  printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$(date -u -d "@$2" +%Y-%m-%dT%H:%M:%SZ)" "$(date -u -d "@$now" +%Y-%m-%dT%H:%M:%SZ)" "$((now - $2))" "$3" >> "$phases"
}

if gcloud container clusters describe "$cluster" --project "$PROJECT" --zone "$zone" > /dev/null 2>&1; then
  echo "gke: cluster ${cluster} already exists in ${zone}; reusing it" >&2
else
  echo "gke: creating cluster ${cluster} in ${zone} (${image} nodes)" >&2
  start=$(date +%s)
  gcloud container clusters create "$cluster" --project "$PROJECT" --zone "$zone" \
    --num-nodes 1 --machine-type e2-small --image-type "$image" --labels purpose=topology-capture \
    --no-enable-autoupgrade --quiet
  rc=$?
  record gke/cluster_create "$start" "$rc"
  [[ $rc -eq 0 ]] || { echo "gke: cluster creation failed" >&2; exit 1; }
fi
gcloud container clusters get-credentials "$cluster" --project "$PROJECT" --zone "$zone" > /dev/null

# The cluster is deleted however the run ends: normally, on an error, or on Ctrl-C. Capture
# node pools left by a failed capture are removed first, and a cluster busy with a node pool
# operation is retried rather than given up on. A GPU node left running is real money.
agent=${TOPOLOGY_AGENT:-topology-agent}
teardown() {
  trap - EXIT INT TERM
  [[ ${KEEP_CLUSTER:-0} != 1 ]] || { echo "gke: KEEP_CLUSTER=1, leaving cluster ${cluster}" >&2; return 0; }
  echo "gke: removing capture node pools left in ${cluster}" >&2
  "$agent" cleanup --provider gke --cluster "$cluster" --project "$PROJECT" --delete >&2
  echo "gke: deleting cluster ${cluster}" >&2
  local start rc attempt
  start=$(date +%s)
  for attempt in $(seq 1 30); do
    gcloud container clusters delete "$cluster" --project "$PROJECT" --zone "$zone" --quiet
    rc=$?
    [[ $rc -ne 0 ]] || break
    gcloud container clusters describe "$cluster" --project "$PROJECT" --zone "$zone" > /dev/null 2>&1 || { rc=0; break; }
    echo "gke: cluster delete failed (attempt ${attempt}/30); retrying in 60s" >&2
    sleep 60
  done
  record gke/cluster_delete "$start" "$rc"
  if [[ $rc -ne 0 ]]; then
    echo "gke: CLUSTER ${cluster} IS STILL RUNNING AND COSTING MONEY. Delete it with:" >&2
    echo "  gcloud container clusters delete ${cluster} --project ${PROJECT} --zone ${zone} --quiet" >&2
    exit 1
  fi
}
trap teardown EXIT
trap 'echo "gke: interrupted" >&2; exit 130' INT TERM

start=$(date +%s)
DATA=$out/gke PROVIDER=gke CLUSTER=$cluster IMAGE_TYPE=$image SKIP_CAPTURED=${SKIP_CAPTURED:-1} bash topology/run-plan.sh "$plan" "$out/captures.log"
rc=$?
record gke/plan "$start" "$rc"
DATA=$out/gke bash topology/render.sh
go run ./cmd/catalog --data "$out/gke" --index-only

teardown

compare=${COMPARE:-}
if [[ -z $compare ]]; then
  for d in projects/$PROJECT/agent/*/deterministic; do
    [[ -d $d/topology ]] && compare="vm=$d" && break
  done
fi
args=()
for d in $compare; do args+=(--data "$d"); done
go run ./experiments/cmd/consistency --data "gke=$out/gke" "${args[@]}" --format json > "$out/consistency.json" &&
  go run ./experiments/cmd/consistency --data "gke=$out/gke" "${args[@]}" > "$out/consistency.md"
go run ./experiments/cmd/latency --data "gke=$out/gke" "${args[@]}" --phases "$phases" --format json > "$out/latency.json" &&
  go run ./experiments/cmd/latency --data "gke=$out/gke" "${args[@]}" --phases "$phases" | tee "$out/latency.md"
echo "gke: done; reports in ${out}/consistency.md and ${out}/latency.md" >&2
