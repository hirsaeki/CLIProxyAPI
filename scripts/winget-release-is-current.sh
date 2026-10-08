#!/usr/bin/env bash
# Print whether a numeric release tag can replace the existing manifest version.
set -euo pipefail
export LC_ALL=C

if [[ $# -ne 2 || ! "$1" =~ ^v[0-9]+([.][0-9]+)+$ ]]; then
  echo "usage: $0 <v-numeric-release-tag> <version-manifest>" >&2
  exit 2
fi
candidate="${1#v}"
manifest="$2"
if [[ ! -f "$manifest" ]]; then
  echo "version manifest not found: $manifest" >&2
  exit 2
fi
current="$(sed -n 's/^PackageVersion: *//p' "$manifest" | tr -d '\r')"
if [[ ! "$current" =~ ^[0-9]+([.][0-9]+)+$ ]]; then
  echo "expected exactly one numeric PackageVersion in $manifest" >&2
  exit 2
fi
IFS=. read -r -a candidate_parts <<< "$candidate"
IFS=. read -r -a current_parts <<< "$current"
count=${#candidate_parts[@]}
if (( ${#current_parts[@]} > count )); then count=${#current_parts[@]}; fi
for ((i=0; i<count; i++)); do
  left="${candidate_parts[i]:-0}"
  right="${current_parts[i]:-0}"
  # Compare decimal strings without octal interpretation or integer overflow.
  while [[ ${#left} -gt 1 && "$left" == 0* ]]; do left="${left#0}"; done
  while [[ ${#right} -gt 1 && "$right" == 0* ]]; do right="${right#0}"; done
  if (( ${#left} > ${#right} )) || { [[ ${#left} -eq ${#right} && "$left" > "$right" ]]; }; then
    echo true
    exit 0
  fi
  if (( ${#left} < ${#right} )) || { [[ ${#left} -eq ${#right} && "$left" < "$right" ]]; }; then
    echo false
    exit 0
  fi
done
echo true
