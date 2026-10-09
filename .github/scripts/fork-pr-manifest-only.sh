#!/usr/bin/env bash
# Print true only when the actual PR diff changes existing WinGet manifest paths.
set -euo pipefail

if [[ $# -ne 2 || ! "$1" =~ ^[0-9a-fA-F]{40}$ || ! "$2" =~ ^[0-9a-fA-F]{40}$ ]]; then
  echo "usage: $0 <base-commit-sha> <head-commit-sha>" >&2
  exit 2
fi
base="$1"
head="$2"
git cat-file -e "${base}^{commit}"
git cat-file -e "${head}^{commit}"
changed_paths="$(mktemp)"
trap 'rm -f "$changed_paths"' EXIT
# Disable rename detection so moving a source file into winget cannot hide it.
git diff --no-renames --name-only -z "${base}...${head}" -- > "$changed_paths"

manifest_only=false
while IFS= read -r -d '' path; do
  case "$path" in
    winget/hirsaeki.CLIProxyAPI.yaml|winget/hirsaeki.CLIProxyAPI.installer.yaml|winget/hirsaeki.CLIProxyAPI.locale.en-US.yaml)
      manifest_only=true
      ;;
    *)
      echo false
      exit 0
      ;;
  esac
done < "$changed_paths"
echo "$manifest_only"
