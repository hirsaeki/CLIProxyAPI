# Fork Maintenance Guide

This fork adds release automation, local model overlays, OAuth model availability,
and plugin capabilities that are not all available upstream. The maintenance goal
is to preserve those features while keeping routine upstream merges small and
reviewable.

## Core Rules

1. Treat the current upstream layout as authoritative. If upstream splits or
   relocates a file, port the fork integration to the new layout instead of
   retaining the old implementation.
2. Put fork behavior in fork-owned files. Modify upstream-owned files only at
   narrow construction, lifecycle, or data-flow seams.
3. Never resolve a structural conflict by selecting the complete `ours` or
   `theirs` version without reviewing the fork delta against the merge base.
4. Preserve behavior with focused tests before changing an integration seam.
5. Prefer a small, generally useful upstream hook over a growing fork patch.

## Ownership Boundaries

Fork-owned files contain the implementation and most tests for fork features.
They may be changed freely when the feature requires it, while still following
the repository's compatibility rules.

Current model-discovery and availability files include:

- `sdk/pluginabi/model_provider_native_candidates.go`
- `internal/pluginhost/rpc_native_model_candidates_test.go`
- `sdk/cliproxy/oauth_model_availability.go`
- `sdk/cliproxy/oauth_model_availability_test.go`
- `sdk/cliproxy/model_registration_extensions.go`
- `sdk/cliproxy/service_plugin_models_test.go`
- `cmd/sync_oauth_model_availability/`
- `examples/plugin/vertex-region-models/`
- `docs/oauth-model-availability.md`
- `docs/vertex-region-model-discovery-implementation.md`

The following upstream-owned files are approved integration seams. Changes in
them must stay local to the listed responsibility.

| File | Allowed fork integration |
| --- | --- |
| `internal/config/config.go` | The `OAuthModelAvailabilityFile` configuration field |
| `sdk/cliproxy/service.go` | Immutable OAuth availability state fields only |
| `sdk/cliproxy/builder.go` | Load and inject the startup availability snapshot |
| `sdk/cliproxy/service_config.go` | Warn when a startup-only availability path changes |
| `sdk/cliproxy/service_models.go` | Pass native candidates through the fork model-registration seam |
| `sdk/cliproxy/service_executors.go` | Pass candidates to per-auth plugin model resolution |
| `internal/pluginhost/adapters.go` | Match explicit model-provider identifiers and transport candidate models |
| `internal/pluginhost/rpc_schema.go` | Transport host features and model-provider identifiers over RPC |
| `internal/pluginhost/rpc_client.go` | Advertise native-candidate support and preserve model-provider identifiers |
| `sdk/pluginapi/types.go` | Candidate-model and provider-identifier API fields |
| `.github/workflows/auto-retarget-main-pr-to-dev.yml` | Keep same-repository `automation/winget-*` release PRs targeting `main` |

Adding another upstream-owned integration file requires documenting why an
existing seam cannot support the feature.

## Model Registration Contract

The model registration order is intentional:

1. Resolve the provider's native catalog and configuration overrides.
2. Apply the authoritative per-credential OAuth availability snapshot.
3. Apply excluded-model rules.
4. Allow a matching plugin to filter or enrich the native candidates.
5. Apply OAuth model aliases and model prefixes.
6. Do not append static plugin models when an authoritative OAuth availability
   entry exists.

Providers without a native catalog must still allow plugin model discovery with
an empty candidate list. OAuth availability is startup-only: a configured
missing or invalid file prevents startup, and a path change requires restart.

## Upstream Sync Procedure

1. Start from a clean fork branch and fetch both `origin` and upstream.
2. Record the fork head, upstream head, and merge base.
3. Inspect fork-only changes with `git diff <merge-base>..HEAD` before merging.
4. Merge upstream without committing automatically when conflicts are expected.
5. For an upstream file split, restore the upstream version and port only the
   documented seam to its new responsibility file.
6. Inspect automatically merged former monoliths as carefully as textual
   conflicts. A clean textual merge can still leave duplicated or misplaced
   declarations after a split.
7. Run the focused fork regression tests before broad repository tests.
8. Review the final diff against upstream and confirm that fork behavior remains
   concentrated in fork-owned files and documented seams.

The scheduled sync workflow intentionally fails on conflicts. Resolve the merge
locally, test it, and push the resulting merge commit; do not weaken the workflow
to hide conflicts.

## Required Regression Checks

For model availability or plugin candidate changes, verify at least:

- valid, missing, malformed, and unsupported availability documents;
- authoritative Claude and xAI OAuth model filtering;
- API-key and unmatched-credential fallback to the native catalog;
- exclusion and alias ordering;
- native Vertex candidates passed to a matching plugin with metadata intact;
- plugin-only providers receiving an empty native candidate list;
- plugin ABI/RPC feature negotiation and candidate cloning;
- Windows synchronous plugin calls and callback lifetime tests.

After focused tests, run:

```bash
gofmt -w .
go test ./...
go build -o test-output ./cmd/server && rm test-output
```

## Upstream Contribution Candidates

The highest-value upstream contribution is a generic per-auth model candidate
resolver seam. It should let a model provider receive the native candidates and
return a filtered or enriched list without requiring provider-specific logic in
`Service.registerModelsForAuthWithCache`.

The upstream PR retarget workflow should also support an explicit exemption for
trusted, same-repository release automation branches. Until then, keep the
`automation/winget-*` guard narrow enough that an external fork cannot bypass
the normal `main` to `dev` retarget policy.

Until such a hook is accepted upstream:

- keep `model_registration_extensions.go` as the fork-owned policy boundary;
- keep call-site changes in upstream-owned service files minimal;
- avoid adding sidecar knowledge to `internal/pluginhost` or provider executors;
- update this section when a recurring patch can be removed or submitted
  upstream.

If upstream accepts an equivalent hook, migrate the fork implementation to it,
remove redundant call-site patches, retain the behavior tests, and update the
ownership table in the same change.
