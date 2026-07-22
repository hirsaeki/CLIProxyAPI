#!/usr/bin/env bash

set -euo pipefail

api_key="${CLIPROXY_API_KEY:-}"
base_url="${CLIPROXY_BASE_URL:-https://cliproxyapi.dev.localhost}"
model="${CLIPROXY_MODEL:-gemini-3.1-flash-lite}"
timeout_seconds="${CLIPROXY_TIMEOUT_SECONDS:-360}"
insecure="${CLIPROXY_INSECURE:-1}"

if [[ -z "$api_key" ]]; then
  echo "CLIPROXY_API_KEY is required" >&2
  exit 2
fi
if [[ -z "$base_url" ]]; then
  echo "CLIPROXY_BASE_URL must not be empty" >&2
  exit 2
fi
if [[ ! "$model" =~ ^[A-Za-z0-9._-]+$ ]]; then
  echo "CLIPROXY_MODEL contains unsupported characters: $model" >&2
  exit 2
fi
if [[ ! "$timeout_seconds" =~ ^[1-9][0-9]*$ ]]; then
  echo "CLIPROXY_TIMEOUT_SECONDS must be a positive integer" >&2
  exit 2
fi
if [[ "$insecure" != "0" && "$insecure" != "1" ]]; then
  echo "CLIPROXY_INSECURE must be 0 or 1" >&2
  exit 2
fi

base_url="${base_url%/}"
request_body='{"contents":[{"role":"user","parts":[{"text":"Reply with exactly OK."}]}],"generationConfig":{"temperature":0,"maxOutputTokens":16}}'

curl_args=(
  --silent
  --show-error
  --fail-with-body
  --request POST
  --retry 0
  --connect-timeout 10
  --max-time "$timeout_seconds"
  --header "Content-Type: application/json"
  --header "X-Goog-Api-Key: $api_key"
  --header "X-Server-Timeout: $timeout_seconds"
  --data-binary "$request_body"
  --write-out $'\nHTTP_STATUS=%{http_code}\n'
)

if [[ "$insecure" == "1" ]]; then
  curl_args+=(--insecure)
fi

curl "${curl_args[@]}" \
  "$base_url/v1beta/models/$model:generateContent"
