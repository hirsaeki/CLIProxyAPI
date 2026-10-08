#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
manifest="$tmp_dir/version.yaml"
check() {
  printf 'PackageVersion: %s\n' "$2" > "$manifest"
  actual="$(bash "$repo_root/scripts/winget-release-is-current.sh" "$1" "$manifest")"
  [[ "$actual" == "$3" ]] || { echo "$1 vs $2: expected $3, got $actual" >&2; exit 1; }
}
check v8.0.19 8.0.20 false
check v8.0.20 8.0.19 true
check v8.0.20 8.0.20 true
check v8.0.9 8.0.10 false
check v8.0.10 8.0.9 true
check v7.2.92.1 7.2.92.2 false
check v7.2.92.2 7.2.92.1 true
check v8.0.20.0 8.0.20 true
check v8.0.20 8.0.20.1 false
check v08.00.020 8.0.20 true
check v8.0.999999999999999999999999 8.0.1000000000000000000000000 false
printf 'PackageVersion: 8.0.20\r\n' > "$manifest"
[[ "$(bash "$repo_root/scripts/winget-release-is-current.sh" v8.0.20 "$manifest")" == true ]]
for content in '' 'PackageVersion: invalid' $'PackageVersion: 8.0.19\nPackageVersion: 8.0.20'; do
  printf '%s\n' "$content" > "$manifest"
  if bash "$repo_root/scripts/winget-release-is-current.sh" v8.0.20 "$manifest" >/dev/null 2>&1; then
    echo "invalid manifest accepted" >&2; exit 1
  fi
done
printf 'PackageVersion: 8.0.20\n' > "$manifest"
for tag in v8.0.20-rc1 invalid v8; do
  if bash "$repo_root/scripts/winget-release-is-current.sh" "$tag" "$manifest" >/dev/null 2>&1; then
    echo "invalid tag accepted" >&2; exit 1
  fi
done
if bash "$repo_root/scripts/winget-release-is-current.sh" v8.0.20 "$tmp_dir/missing" >/dev/null 2>&1; then
  echo "missing manifest accepted" >&2; exit 1
fi

# Execute the actual post-check revalidation block with read-only command stubs.
# The remote manifest advances after the initial version check; merge must not run.
mkdir -p "$tmp_dir/bin"
cat > "$tmp_dir/bin/git" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  fetch) [[ "$*" == 'fetch --no-tags origin main' ]] ;;
  show) [[ "$2" == FETCH_HEAD:winget/hirsaeki.CLIProxyAPI.yaml ]]; cat "$REMOTE_MANIFEST" ;;
  *) exit 1 ;;
esac
STUB
cat > "$tmp_dir/bin/gh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == 'pr merge --merge --delete-branch 123' ]]
echo merged >> "$MERGE_LOG"
STUB
chmod +x "$tmp_dir/bin/git" "$tmp_dir/bin/gh"
awk '/# Checks may take hours/ {copy=1} copy {sub(/^          /, ""); print}' \
  "$repo_root/.github/workflows/release.yaml" > "$tmp_dir/premerge.sh"
grep -Fq 'git fetch --no-tags origin "$DEFAULT_BRANCH"' "$tmp_dir/premerge.sh"
grep -Fq 'gh pr merge' "$tmp_dir/premerge.sh"
export REMOTE_MANIFEST="$manifest" MERGE_LOG="$tmp_dir/merge.log"
export DEFAULT_BRANCH=main PR_NUMBER=123 GITHUB_REF_NAME=v8.0.19
export PATH="$tmp_dir/bin:$PATH"
cd "$repo_root"
printf 'PackageVersion: 8.0.20\n' > "$manifest"
bash -euo pipefail "$tmp_dir/premerge.sh"
[[ ! -e "$MERGE_LOG" ]] || { echo 'stale release merged' >&2; exit 1; }
printf 'PackageVersion: 8.0.19\n' > "$manifest"
bash -euo pipefail "$tmp_dir/premerge.sh"
[[ "$(cat "$MERGE_LOG")" == merged ]]
printf 'PackageVersion: invalid\n' > "$manifest"
if bash -euo pipefail "$tmp_dir/premerge.sh" >/dev/null 2>&1; then
  echo 'invalid remote manifest accepted' >&2; exit 1
fi
[[ "$(wc -l < "$MERGE_LOG")" -eq 1 ]]
echo 'WinGet release version guards passed'
