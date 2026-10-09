#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
workflow="$repo_root/.github/workflows/pr-test-build.yml"

require_line() {
  grep -Fq -- "$1" "$workflow" || { echo "missing PR workflow contract: $1" >&2; exit 1; }
}
require_count() {
  local actual
  actual="$(grep -Fc -- "$2" "$workflow" || true)"
  [[ "$actual" -eq "$1" ]] || { echo "expected $1 instances of $2, found $actual" >&2; exit 1; }
}
require_step_condition() {
  local step="$1" condition="$2" block
  block="$(awk -v step="$step" '
    $0 == "      - name: " step { in_step=1; next }
    in_step && /^      - / { exit }
    in_step { print }
  ' "$workflow")"
  grep -Fxq "        if: $condition" <<< "$block" || {
    echo "missing condition for $step: $condition" >&2; exit 1
  }
}

require_line 'bash .github/scripts/fork-pr-manifest-only.sh "$BASE_SHA" "$HEAD_SHA"'
require_line 'BASE_SHA: ${{ github.event.pull_request.base.sha }}'
require_line 'HEAD_SHA: ${{ github.event.pull_request.head.sha }}'
require_line 'name: build Windows plugin (${{ matrix.goarch }})'
require_line "runs-on: \${{ needs.changes.outputs.manifest_only == 'true' && matrix.goarch == 'arm64' && 'ubuntu-latest' || matrix.runner }}"
require_count 2 '    needs: changes'
require_count 2 '    if: ${{ !cancelled() }}'
require_count 2 "if: needs.changes.result != 'success' || (needs.changes.outputs.manifest_only != 'true' && needs.changes.outputs.manifest_only != 'false')"
require_count 2 '        uses: actions/setup-go@v5'
awk -v condition="        if: needs.changes.outputs.manifest_only != 'true'" '
  /uses: actions\/setup-go@v5/ && previous != condition { exit 1 }
  { previous=$0 }
' "$workflow" || { echo 'Go setup must be skipped for manifest-only changes' >&2; exit 1; }
for step in 'Build' 'Refresh models catalog' 'Install LLVM-MinGW' 'Build Vertex region models plugin'; do
  require_step_condition "$step" "needs.changes.outputs.manifest_only != 'true'"
done
require_step_condition 'Validate WinGet manifest' "needs.changes.outputs.manifest_only == 'true' && matrix.goarch == 'amd64'"
require_step_condition 'Test Windows Vertex plugin runtime' "\${{ needs.changes.outputs.manifest_only != 'true' && matrix.goarch == 'amd64' }}"
require_line 'winget validate --manifest winget'
require_line 'cancel-in-progress: true'
echo 'Manifest-only PR workflow checks passed'
