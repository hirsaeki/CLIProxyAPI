#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/bin" "$tmp_dir/capture"
cat > "$tmp_dir/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

count_file="$CURL_CAPTURE_DIR/count"
count=0
if [[ -f "$count_file" ]]; then
  count="$(<"$count_file")"
fi
printf '%s\n' "$((count + 1))" > "$count_file"
printf '%s\n' "$@" > "$CURL_CAPTURE_DIR/args"
printf '{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}\nHTTP_STATUS=200\n'
EOF
chmod +x "$tmp_dir/bin/curl"

output="$(
  PATH="$tmp_dir/bin:$PATH" \
    CURL_CAPTURE_DIR="$tmp_dir/capture" \
    CLIPROXY_API_KEY="test-api-key" \
    CLIPROXY_BASE_URL="https://proxy.example.test" \
    CLIPROXY_MODEL="gemini-test-model" \
    "$repo_root/scripts/probe-gemini-model-once.sh"
)"

if [[ "$(<"$tmp_dir/capture/count")" != "1" ]]; then
  echo "expected curl to be called exactly once" >&2
  exit 1
fi

assert_arg() {
  local expected="$1"
  if ! grep -Fqx -- "$expected" "$tmp_dir/capture/args"; then
    printf 'expected curl argument not found: %s\n' "$expected" >&2
    exit 1
  fi
}

assert_arg "--retry"
assert_arg "0"
assert_arg "X-Goog-Api-Key: test-api-key"
assert_arg "https://proxy.example.test/v1beta/models/gemini-test-model:generateContent"

if [[ "$output" != *"HTTP_STATUS=200"* ]]; then
  echo "expected HTTP status in script output" >&2
  exit 1
fi

rm -f "$tmp_dir/capture/count" "$tmp_dir/capture/args"
if PATH="$tmp_dir/bin:$PATH" \
  CURL_CAPTURE_DIR="$tmp_dir/capture" \
  env -u CLIPROXY_API_KEY "$repo_root/scripts/probe-gemini-model-once.sh" 2>/dev/null; then
  echo "script unexpectedly accepted a missing CLIPROXY_API_KEY" >&2
  exit 1
fi
if [[ -e "$tmp_dir/capture/count" ]]; then
  echo "curl was called without an API key" >&2
  exit 1
fi
