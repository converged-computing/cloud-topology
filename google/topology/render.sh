#!/usr/bin/env bash
# Renders topology.svg for captures in $DATA/topology, using hwloc's lstopo-no-graphics
# with a legend from capture.json. Without arguments every capture that has no image, or a
# capture newer than its image, is rendered.
#
# Usage: DATA=data topology/render.sh [--force] [MACHINE_TYPE ...]
set -euo pipefail

data=${DATA:-data}
force=0
types=()
for arg in "$@"; do
  case $arg in
    --force) force=1 ;;
    -h|--help) sed -n '2,6p' "$0"; exit 0 ;;
    *) types+=("$arg") ;;
  esac
done
if [[ ${#types[@]} -eq 0 ]]; then
  for dir in "$data"/topology/*/; do
    [[ -d $dir ]] && types+=("$(basename "$dir")")
  done
fi

command -v lstopo-no-graphics > /dev/null || { echo "lstopo-no-graphics not found (install hwloc)" >&2; exit 1; }

rendered=0
for mt in "${types[@]}"; do
  dir=$data/topology/$mt
  xml=$dir/hwloc.xml
  svg=$dir/topology.svg
  if [[ ! -f $xml ]]; then
    echo "render: ${mt}: no hwloc.xml" >&2
    continue
  fi
  if [[ $force -eq 0 && -f $svg && ! $xml -nt $svg ]]; then
    continue
  fi
  legend=("$mt")
  if [[ -f $dir/capture.json ]]; then
    while IFS=$'\t' read -r key value; do
      [[ -n $value && $value != null ]] && legend+=("${key}: ${value}")
    done < <(jq -r '{"captured": .captured_at, "model": .provisioning_model, "hwloc": .hwloc_version} | to_entries[] | [.key, .value] | @tsv' "$dir/capture.json")
  fi
  args=()
  for line in "${legend[@]}"; do
    args+=(--append-legend "$line")
  done
  # lstopo writes px units into the viewBox, which some renderers reject, and stamps the
  # render time into the legend; drop the units and the stamp so re-renders are identical.
  lstopo-no-graphics --input "$xml" --of svg "${args[@]}" "$svg.tmp"
  sed -E -e "s/viewBox='([0-9.]+) ([0-9.]+) ([0-9.]+)px ([0-9.]+)px'/viewBox='\1 \2 \3 \4'/" \
         -e "/>Date: [^<]*<\/text>/d" "$svg.tmp" > "$svg"
  rm -f "$svg.tmp"
  rendered=$((rendered + 1))
done
echo "render: ${rendered} image(s) in ${data}/topology" >&2
