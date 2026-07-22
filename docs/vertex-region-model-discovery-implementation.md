# Vertex Region Model Discovery Implementation

## Status

Completed on 2026-07-20.
Windows plugin distribution revised on 2026-07-22.
Authoritative model discovery revised on 2026-07-22.
Windows plugin runtime safety revised on 2026-07-22.

## Problem

Vertex model availability currently comes from the shared model catalog rather
than the location configured on each Vertex credential. This can advertise a
model for a credential whose endpoint rejects it, and remote catalog refreshes
can overwrite local attempts to correct the list.

The runtime also constructs the wrong service hostname for the `us` and `eu`
multi-region locations. They use the regional Google API hostname form rather
than the documented `.rep.googleapis.com` hostnames.

## Goals

- Resolve Vertex model availability independently for each credential location.
- Treat Google's location matrix and its last successfully parsed cache as the
  authoritative source of Vertex model IDs for each credential.
- Reuse host model metadata where it matches without allowing the host catalog
  to suppress documented models.
- Keep location discovery, HTML parsing, caching, and failure policy outside the
  native Vertex implementation so upstream synchronization remains small.
- Correct native Vertex request endpoints for `global`, `us`, `eu`, and regular
  regional locations.
- Keep existing plugin and native-provider behavior backward compatible.
- Support executable-relative plugin discovery for layouts where the executable
  and plugin tree share a stable directory.
- Publish compatible Windows Vertex plugin ZIPs separately from the server and
  WinGet archives without enabling trusted plugin code by default.

## Non-goals

- Replacing the native Vertex executor with a plugin executor.
- Disabling the remote model catalog updater.
- Removing the local model overlay escape hatch.
- Treating the documentation HTML as an infallible provider API contract.

## Design

### Plugin API additions

Add `ModelProviderIdentifiers` to plugin capabilities. It explicitly binds a
model provider plugin to existing auth provider identifiers such as `vertex`
without requiring the plugin to claim auth refresh/login or declare a dummy
executor.

Add `CandidateModels` to `AuthModelRequest`. For native providers, the service
first resolves the credential's normal model list, including configured model
overrides, exclusions, and provider-specific capability enrichment. The plugin
uses those models only as optional metadata donors. They do not constrain the
IDs returned from the documented location matrix.

Custom plugin providers keep the existing pre-native discovery path with an
empty candidate list.

The host includes `model-provider-native-candidates` in the optional
`host_features` lifecycle field. The Vertex plugin advertises its model provider
capability only when that feature is present. An old host can therefore load the
new DLL safely as an inert plugin instead of invoking it with an empty candidate
list. This is an additive schema-1 extension rather than a global schema version
increase, so unrelated existing plugins remain compatible.

### Vertex region model plugin

Add a Go C-ABI plugin under `examples/plugin/vertex-region-models/go`.

The plugin:

1. Declares `model_provider` and binds it to `vertex`.
2. Reads `location` from auth metadata, then persisted credential JSON, with
   `us-central1` as the compatibility default.
3. Fetches the official Google locations page through the host HTTP callback.
4. Parses location columns and cells whose `aria-label` is `Supported` from the
   `Gemini models` sections, excluding separate embedding and media APIs.
5. Returns the documented supported IDs in page order. Exact candidates retain
   their metadata, documented `-preview` IDs can reuse a non-preview candidate,
   and documentation-only IDs receive conservative identity metadata.
6. Caches the last successful matrix in memory with a configurable TTL and uses
   conditional `ETag` or `Last-Modified` requests when available.
7. Defaults to fail-closed behavior: before the first successful fetch, a fetch
   error, parse error, or unknown location returns an empty model list. Explicit
   `fail_open: true` retains the legacy native-candidate fallback.
8. Reports resolved location/model counts and discovery failures through the
   host logging callback without credential contents.

The default source is:

`https://docs.cloud.google.com/gemini-enterprise-agent-platform/resources/locations`

Supported plugin configuration fields:

- `docs_url`: alternate matrix URL, primarily for controlled mirrors and tests.
- `cache_ttl_seconds`: positive cache lifetime.
- `fail_open`: whether discovery failures explicitly fall back to all native
  candidate models; defaults to `false`.

### Vertex endpoint resolution

The native executor uses these hostnames:

| Location | Hostname |
| --- | --- |
| `global` | `https://aiplatform.googleapis.com` |
| `us` | `https://aiplatform.us.rep.googleapis.com` |
| `eu` | `https://aiplatform.eu.rep.googleapis.com` |
| other region | `https://<location>-aiplatform.googleapis.com` |

### Plugin discovery and Windows distribution

`plugins.dir` accepts `@exe` and `@exe/<relative-path>`. The token resolves to
the directory containing the running executable, accepts either slash style,
and rejects paths that escape that directory through `..`. Existing empty,
tilde, absolute, and working-directory-relative values keep their prior
behavior.

Windows releases publish separate x64 and ARM64 plugin ZIPs with this layout:

```text
plugins/
  windows/
    amd64|arm64/
      vertex-region-models-v<release-version>.dll
```

The plugin ZIP is extracted into `~/.cli-proxy-api`, and `plugins.dir` points to
`~/.cli-proxy-api/plugins`. This location remains stable when WinGet launches
the server through an alias under `Microsoft\WinGet\Links`. The server archives
and WinGet portable package contain no plugin DLL. WinGet does not modify user
configuration; both the global plugin host and `vertex-region-models` must be
enabled explicitly.

## Failure and Concurrency Behavior

- Discovery cache access is synchronized because auth registration runs in
  parallel.
- Only a successfully parsed non-empty location matrix replaces the cached
  matrix.
- A `304 Not Modified` response refreshes the cache lifetime without reparsing.
- A refresh failure keeps the last successfully parsed matrix authoritative and
  retries after at most five minutes.
- No credential contents, access tokens, or service-account fields are logged.
- Existing model exclusions, aliases, and credential prefixes are still applied
  by the host registration path.

## Implementation Steps

- [x] Add plugin capability and RPC schema support for explicit model provider
  identifiers.
- [x] Add native candidate models to auth model discovery requests.
- [x] Move native-provider plugin model discovery after candidate construction
  while preserving custom plugin provider behavior.
- [x] Add unit tests for provider matching, RPC schema propagation, candidate
  metadata reuse, and authoritative model discovery.
- [x] Correct and test Vertex multi-region endpoint construction.
- [x] Implement and test location extraction, ordered HTML model parsing, cache
  behavior, fail-open/fail-closed behavior, metadata reuse, and
  documentation-only model synthesis in the Go plugin.
- [x] Document plugin build and configuration in `examples/plugin/README.md`.
- [x] Add executable-relative plugin directory resolution with escape checks.
- [x] Add lifecycle host feature negotiation and make old-host registration inert.
- [x] Publish versioned Windows x64/ARM64 plugin ZIPs separately from server archives and add matching PR CI builds.
- [x] Serialize Windows Go C-shared calls, move blocking host callback work off
  the foreign callback stack, and run concurrent amd64 DLL integration tests.
- [x] Keep the WinGet nested portable list limited to the server executable.
- [x] Reconcile configuration, plugin, WinGet, and lifecycle documentation with the implementation.
- [x] Run formatting, focused tests, plugin build, the full Go test suite, and
  the required server compile check.

## Verification Gates

- `go test ./internal/pluginhost ./sdk/cliproxy ./internal/runtime/executor`
- `go test ./...`
- `go test ./...` from `examples/plugin/vertex-region-models/go`
- `go build -buildmode=c-shared` for the Vertex region model plugin
- `bash scripts/tests/generate-winget-manifest_test.sh`
- `bash scripts/tests/windows-plugin-workflow_test.sh`
- Windows amd64 execution of `TestDynamicLibraryClientSerializesCalls` and
  `TestVertexRegionModelsPluginCABI` against the packaged DLL
- YAML parsing for the release, PR build, and example configuration files
- `go build -o test-output ./cmd/server` followed by removal of `test-output`
- `gofmt` leaves all changed Go files formatted

## Risks and Unknowns

- The documentation page is HTML, not a stable machine API. The parser therefore
  relies on semantic table content and `aria-label`, not line layout, and the
  cache keeps the last known good matrix.
- Documentation can lag actual provider rollout. Operators can explicitly use
  `fail_open: true`, but that temporarily makes the native catalog authoritative.
- Documentation-only models have intentionally incomplete capability metadata.
  The plugin does not guess token limits, thinking levels, or modalities.
- Windows serializes calls into each Go C-shared plugin. This trades per-plugin
  call concurrency for reliable response-buffer ownership across the native ABI.

## Final Results

The implementation adds a native-provider model hook to the plugin API and uses
it from a model-only Vertex plugin. The native Vertex executor still owns
upstream requests. The plugin makes the credential location's documentation
matrix authoritative for IDs, while resolved native candidates contribute
metadata without limiting which documented models are registered.

The plugin was loaded through the real C ABI in an opt-in integration test. The
test verified registration with `model_provider_identifiers: ["vertex"]`, no
auth-provider or executor capability, host HTTP callback execution, location
selection, `-preview` metadata reuse, documentation-only model registration,
and preservation of thinking and token-limit metadata.

Verification completed successfully:

- `go test ./internal/pluginhost ./sdk/cliproxy ./internal/runtime/executor`
- `go test ./...`
- `go test ./...` from `examples/plugin/vertex-region-models/go`
- `VERTEX_LOCATIONS_HTML=<downloaded-page> go test -v -run
  TestParseLocationMatrixOfficialSnapshot ./...` against the current official
  Google locations page
- `go test -race ./...` from `examples/plugin/vertex-region-models/go`
- `make -C examples/plugin vertex-region-models`
- `make -n build` from `examples/plugin`
- `go build -o test-output ./cmd/server`, followed by removal of `test-output`
- `git diff --check`

The delivered scope does not call an authenticated PublisherModels API.
Documentation-only models are registered with conservative identity metadata;
capability fields remain empty until a safe native donor exists. Cache state is
process-local; a restart performs a fresh fetch, while refresh failures within a
process retain the last successfully parsed matrix.

Windows release builds now cross-compile both plugin architectures on
`windows-latest` with the pinned LLVM-MinGW `20260616` x86_64 host package. The
download is verified against its published SHA-256 digest, and each matrix entry
selects an explicit target compiler (`x86_64-w64-mingw32-clang` or
`aarch64-w64-mingw32-clang`). This avoids depending on the runner's default
`gcc`, which may target a different architecture.

The Go linker writes the requested DLL name into an unquoted `LIBRARY` entry in
its generated DEF file. Because versioned names such as
`vertex-region-models-v7.2.92.dll` are not accepted by all MinGW linkers, CI
links as `vertex_region_models.dll`, removes the generated C header, and then
renames the DLL to the versioned archive name. Pull requests exercise the same
amd64 and arm64 cross-build and plugin ZIP layout. The amd64 PR and release jobs
also load the packaged DLL and verify five concurrent model-discovery calls
through real host callbacks. Windows serializes native calls per plugin and
dispatches callback work from the foreign callback stack to a regular Go
goroutine, preventing successful calls from returning empty JSON buffers.
Server archives exclude the DLL, while Release notes link directly to both
plugin assets. A workflow regression test locks down the toolchain, compiler
mapping, packaging split, runtime test, and rename behavior. Local verification
also covered executable-relative path resolution, missing-feature behavior for
both register and reconfigure, the manifest's DLL-alias exclusion, and a native
`c-shared` build on the development host.
