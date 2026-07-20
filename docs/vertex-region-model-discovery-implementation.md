# Vertex Region Model Discovery Implementation

## Status

Completed on 2026-07-20.

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
- Preserve the complete model metadata supplied by the host catalog while
  filtering model IDs against Google's location matrix.
- Keep location discovery, HTML parsing, caching, and failure policy outside the
  native Vertex implementation so upstream synchronization remains small.
- Correct native Vertex request endpoints for `global`, `us`, `eu`, and regular
  regional locations.
- Keep existing plugin and native-provider behavior backward compatible.
- Discover bundled plugins relative to the executable instead of the caller's
  working directory.
- Ship the host and compatible Vertex plugin together in Windows release and
  WinGet archives without enabling trusted plugin code by default.

## Non-goals

- Replacing the native Vertex executor with a plugin executor.
- Disabling the remote model catalog updater.
- Removing the local model overlay escape hatch.
- Treating the documentation HTML as an infallible provider API contract.
- Adding models that are absent from the host's current Vertex catalog.

## Design

### Plugin API additions

Add `ModelProviderIdentifiers` to plugin capabilities. It explicitly binds a
model provider plugin to existing auth provider identifiers such as `vertex`
without requiring the plugin to claim auth refresh/login or declare a dummy
executor.

Add `CandidateModels` to `AuthModelRequest`. For native providers, the service
first resolves the credential's normal model list, including configured model
overrides, exclusions, and provider-specific capability enrichment. The plugin
then receives those models and returns a filtered subset. Returning full model
objects preserves thinking limits, token limits, modalities, and other metadata.

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
4. Parses location columns and cells whose `aria-label` is `Supported`.
5. Intersects the discovered model IDs with `CandidateModels` and returns the
   original candidate objects unchanged.
6. Caches the last successful matrix in memory with a configurable TTL and uses
   conditional `ETag` or `Last-Modified` requests when available.
7. Defaults to fail-open behavior: a fetch error, parse error, or unknown
   location returns the unfiltered candidates. A successful known location with
   no matching candidates returns an empty model list.

The default source is:

`https://docs.cloud.google.com/gemini-enterprise-agent-platform/resources/locations`

Supported plugin configuration fields:

- `docs_url`: alternate matrix URL, primarily for controlled mirrors and tests.
- `cache_ttl_seconds`: positive cache lifetime.
- `fail_open`: whether discovery failures retain all candidate models.

### Vertex endpoint resolution

The native executor uses these hostnames:

| Location | Hostname |
| --- | --- |
| `global` | `https://aiplatform.googleapis.com` |
| `us` | `https://aiplatform.us.rep.googleapis.com` |
| `eu` | `https://aiplatform.eu.rep.googleapis.com` |
| other region | `https://<location>-aiplatform.googleapis.com` |

### Executable-relative discovery and Windows distribution

`plugins.dir` accepts `@exe` and `@exe/<relative-path>`. The token resolves to
the directory containing the running executable, accepts either slash style,
and rejects paths that escape that directory through `..`. Existing empty,
tilde, absolute, and working-directory-relative values keep their prior
behavior.

Windows x64 and ARM64 archives contain:

```text
cli-proxy-api.exe
plugins/
  windows/
    amd64|arm64/
      vertex-region-models-v<release-version>.dll
```

The WinGet portable installer copies this archive tree but exposes only
`cli-proxy-api.exe` through `NestedInstallerFiles`. The DLL is not a portable
command alias. WinGet does not modify user configuration; both the global plugin
host and `vertex-region-models` must be enabled explicitly.

## Failure and Concurrency Behavior

- Discovery cache access is synchronized because auth registration runs in
  parallel.
- Only a successfully parsed non-empty location matrix replaces the cached
  matrix.
- A `304 Not Modified` response refreshes the cache lifetime without reparsing.
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
  cloning, and native model filtering.
- [x] Correct and test Vertex multi-region endpoint construction.
- [x] Implement and test location extraction, HTML matrix parsing, cache
  behavior, fail-open/fail-closed behavior, and candidate filtering in the Go
  plugin.
- [x] Document plugin build and configuration in `examples/plugin/README.md`.
- [x] Add executable-relative plugin directory resolution with escape checks.
- [x] Add lifecycle host feature negotiation and make old-host registration inert.
- [x] Bundle versioned Windows x64/ARM64 DLLs in release archives and add matching PR CI builds.
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
- YAML parsing for the release, PR build, and example configuration files
- `go build -o test-output ./cmd/server` followed by removal of `test-output`
- `gofmt` leaves all changed Go files formatted

## Risks and Unknowns

- The documentation page is HTML, not a stable machine API. The parser therefore
  relies on semantic table content and `aria-label`, not line layout, and the
  cache keeps the last known good matrix.
- Documentation can lag actual provider rollout. Fail-open avoids removing all
  models during discovery failure, but cannot prove provider availability.
- This implementation deliberately filters the host catalog and does not invent
  docs-only model metadata. A separate authenticated publisher-model discovery
  source would be needed to add models missing from the catalog safely.

## Final Results

The implementation adds a generic model-filter hook to the plugin API and uses
it from a model-only Vertex plugin. Native model discovery still owns model
metadata and the native Vertex executor still owns upstream requests. The
plugin receives the resolved per-credential candidates and returns their
intersection with the credential location's documentation matrix.

The plugin was loaded through the real C ABI in an opt-in integration test. The
test verified registration with `model_provider_identifiers: ["vertex"]`, no
auth-provider or executor capability, host HTTP callback execution, location
filtering, and preservation of thinking and token-limit metadata.

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

The delivered scope does not call an authenticated PublisherModels API and does
not synthesize metadata for documentation-only models. The location matrix is
therefore a filter over the host catalog, not an additional source of model
records. Cache state is process-local; a restart performs a fresh fetch, while
refresh failures within a process retain the last successfully parsed matrix.

Windows release builds now compile the plugin for the host matrix architecture,
remove the generated C header, assert the expected DLL layout, and archive the
whole plugin directory. Pull requests exercise the same native `c-shared` build
on `windows-latest` for amd64 and `windows-11-arm` for arm64. Local verification
also covered executable-relative path resolution, missing-feature behavior for
both register and reconfigure, the manifest's DLL-alias exclusion, and a native
`c-shared` build on the development host.
