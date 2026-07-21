# Vertex Region Models Plugin

This Go C-ABI plugin filters the native Vertex model catalog independently for
each credential location. It fetches Google's endpoint location matrix through
the host HTTP callback and returns only candidate model IDs marked as supported
for the credential's `location`.

The plugin does not replace the native Vertex executor and does not invent model
metadata. Each selected model is returned with the complete host-provided
metadata, including thinking limits, token limits, and modalities.

## Build

From the repository root:

```bash
make -C examples/plugin vertex-region-models
```

The platform extension is `.so` on Linux, `.dylib` on macOS, and `.dll` on
Windows. Alternatively, build directly:

```bash
cd examples/plugin/vertex-region-models/go
go build -buildmode=c-shared -o ../vertex-region-models.so .
```

The Makefile artifact is named `vertex-region-models-go` with the platform
extension. Place it in the configured plugin directory without changing that
basename, or rename it and use the new basename as the plugin configuration key.

Windows releases publish separate x64 and ARM64 plugin ZIPs named
`vertex-region-models_<version>_windows_<asset-arch>.zip`, where the asset
architecture is `amd64` or `aarch64`. Each ZIP contains the versioned DLL under
the Go architecture directory (`plugins/windows/amd64` or
`plugins/windows/arm64`). Extract the matching ZIP into `~/.cli-proxy-api`
(`$HOME\.cli-proxy-api` in PowerShell). The server and WinGet archives do not
contain the plugin.

Release and pull-request builds use a pinned LLVM-MinGW x86_64 host package to
cross-compile both Windows architectures. The matrix selects
`x86_64-w64-mingw32-clang` for amd64 and `aarch64-w64-mingw32-clang` for arm64.
CI links the plugin with the temporary name `vertex_region_models.dll` before
renaming it to the versioned archive name. This is required because Go places
the requested output name in an unquoted generated DEF `LIBRARY` entry, and
some MinGW linkers reject the versioned name.

## Configuration

```yaml
plugins:
  enabled: true
  dir: "~/.cli-proxy-api/plugins"
  configs:
    vertex-region-models:
      enabled: true
      priority: 20
      docs_url: "https://docs.cloud.google.com/gemini-enterprise-agent-platform/resources/locations"
      cache_ttl_seconds: 21600
      fail_open: true
```

Use `dir: "plugins"` and the `vertex-region-models-go` configuration key for
the unrenamed local Makefile artifact. `@exe` remains available for layouts
where the executable and plugin tree share a stable directory, but it is not
recommended for WinGet aliases under `Microsoft\WinGet\Links`.

The plugin advertises its model provider capability only when the host lifecycle
request includes the `model-provider-native-candidates` feature. Loading it on an
older host is therefore a safe no-op instead of replacing Vertex models with an
empty list.

Defaults:

- `docs_url`: the official Google Gemini Enterprise Agent Platform locations page
- `cache_ttl_seconds`: `21600` (six hours)
- `fail_open`: `true`

The plugin reads `location` from runtime auth metadata first, then from persisted
credential JSON, and defaults to `us-central1` for compatibility with the native
Vertex credential loader.

## Discovery and Cache Behavior

The plugin parses location columns from HTML table headers and considers only
cells whose `aria-label` is `Supported`. It intersects those IDs with the
credential's native candidate models; documentation-only IDs are not added.

The last successfully parsed matrix is cached in memory. Refresh requests use
`ETag` and `Last-Modified` validators when the server provides them. A failed
refresh keeps the last known good matrix and retries after at most five minutes.

Before the first successful fetch:

- `fail_open: true` returns all native candidates unchanged.
- `fail_open: false` returns an empty model list.

An unknown credential location follows the same failure policy. A known
location with no matching candidate models returns an empty list.

## Scope and Limitations

- The documentation page is HTML and can change without an API version bump.
- Documentation can lag provider rollout.
- The plugin filters the current host catalog. It cannot add a model that is
  absent from that catalog without losing authoritative model metadata.
- Cache state is process-local and is rebuilt after restart.
