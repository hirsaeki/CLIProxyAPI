#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
workflow_dir="$repo_root/.github/workflows"

assert_excluded() {
  local file="$1" job="$2" condition="$3" block
  block="$(awk -v job="$job" '
    $0 == "  " job ":" { in_job=1; next }
    in_job && /^  [A-Za-z0-9_-]+:$/ { exit }
    in_job { print }
  ' "$workflow_dir/$file")"
  grep -Fxq "    if: $condition" <<< "$block" || {
    echo "missing fork exclusion for $file / $job" >&2
    exit 1
  }
}

fork_exclusion="github.repository != 'hirsaeki/CLIProxyAPI'"
assert_excluded agents-md-guard.yml close-when-agents-md-changed "$fork_exclusion"
assert_excluded pr-path-guard.yml ensure-no-translator-changes "$fork_exclusion"
assert_excluded auto-retarget-main-pr-to-dev.yml retarget "$fork_exclusion && github.actor != 'github-actions[bot]'"
for job in docker_amd64 docker_arm64 docker_manifest; do
  assert_excluded docker-image.yml "$job" "$fork_exclusion"
done

for workflow in sync-upstream.yml docker-image-ghcr.yml release.yaml repository-bundle.yml winget-manifest-test.yml pr-test-build.yml; do
  [[ -s "$workflow_dir/$workflow" ]] || { echo "missing fork workflow: $workflow" >&2; exit 1; }
done
grep -Fq 'cron: "0 */4 * * *"' "$workflow_dir/sync-upstream.yml"
if grep -Fq '"ensure-no-translator-changes"' "$workflow_dir/release.yaml"; then
  echo 'WinGet publication still requires the upstream translator contribution policy' >&2
  exit 1
fi

freebsd_job="$(awk '
  /^  build-freebsd:$/ { in_job=1; next }
  in_job && /^  [A-Za-z0-9_-]+:$/ { exit }
  in_job { print }
' "$workflow_dir/release.yaml")"
grep -Fxq '    timeout-minutes: 20' <<< "$freebsd_job" || {
  echo 'FreeBSD release job must retain its overall time bound' >&2
  exit 1
}
grep -Fq 'curl -fsSL --connect-timeout 15 --max-time 600 --retry 3 --retry-max-time 300 --retry-connrefused "$url" -o /tmp/base.txz' <<< "$freebsd_job" || {
  echo 'FreeBSD sysroot download must allow slow transfers while retaining connection and retry limits' >&2
  exit 1
}
echo 'Fork workflow policy checks passed'
