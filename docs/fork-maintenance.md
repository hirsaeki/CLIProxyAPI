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

Historical Codex capability tests use the scoped fixture in
`internal/testutil/forkcatalog/` when they require retired model templates.
The helper preserves other current entries and restores the exact prior catalog
with a deferred function before test cleanup. Keep it limited to serial tests
without an active service catalog updater. Do not pin the live refresh step or
replace its complete regression suite with the historical fixture.

Fork-owned files contain the implementation and most tests for fork features.
They may be changed freely when the feature requires it, while still following
the repository's compatibility rules.

For new fork-owned Go files, use a feature-specific name ending in `_fork.go`
(or `_fork_test.go` for tests) to reduce collisions with future upstream files.
Keep any Go platform suffix last, for example `feature_fork_windows.go` or
`feature_fork_windows_test.go`, so build selection remains correct. Other
fork-owned files should use an explicit `fork-` prefix where practical.
Do not rename upstream-owned integration seams or bulk-rename existing files;
rename an existing fork-owned file when a collision occurs.

Upstream owns content-addressed plugin shadow copies and resident native-image
lifetime handling in `shadow*.go` and the platform loaders. Keep the fork's two
Windows integration points limited to the synchronous loader wrapper and host
callback bridge; do not restore the shadow-copy helpers to `loader_windows.go`.
Validate resident host blocks, detached callbacks, source replacement, and shadow
reuse alongside the fork's synchronous-call and Vertex plugin runtime checks.

The Vertex multi-region endpoint regression test is fork-owned:
`internal/runtime/executor/gemini_vertex_endpoint_fork_test.go`. Keep it separate
from upstream's `gemini_vertex_executor_test.go`, including its test function name.
Upstream now supports case-insensitive global and us/eu multi-region endpoints;
use that implementation without a fork production delta and retain the fork
regression for default, trimmed, global, and ordinary regional locations.

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
| `internal/config/config.go` | The `OAuthModelAvailabilityFile` configuration field and source-provenance contract |
| `internal/config/config_load.go` | Advance source provenance only for exact successful loader-owned rewrites |
| `internal/registry/model_updater.go` | Apply the local Google model overlay when loading the startup catalog |
| `internal/registry/catalog_sources.go` | Apply the same overlay before change detection and publication for refreshed catalog sources |
| `sdk/cliproxy/service.go` | Immutable OAuth availability state fields only |
| `sdk/cliproxy/auth/conductor_lifecycle.go` | Reject stale new registrations using upstream canonical file provenance inside the existing per-auth mutation gate |
| `sdk/cliproxy/service_auth.go` | Use the committed/current auth returned by manager reconciliation |
| `sdk/cliproxy/builder.go` | Load and inject the startup availability snapshot |
| `sdk/cliproxy/service_config.go` | Warn when a startup-only availability path changes |
| `sdk/cliproxy/service_models.go` | Pass native candidates through the fork model-registration seam and preserve static-plugin exclusions in compatible-provider branches |
| `internal/runtime/executor/claude_executor_stream.go` | Bound complete native and translated Claude SSE events through the fork-owned helper before appending, preserving failed usage on overflow |
| `sdk/api/handlers/claude/code_handlers.go` | Bound partial lines and multi-line SSE events through the fork-owned observer state and existing `ChunkError` hook, including the first chunk before headers |
| `internal/home/cluster_balance.go` | Close dedicated rebalance sockets before Redis pool shutdown so heartbeat cancellation interrupts blocked RPCs |
| `internal/discovery/zeroconf.go` | Keep the host-conflict probe receive buffer large enough for complete mDNS datagrams until the upstream buffer fix lands |
| `sdk/cliproxy/service_executors.go` | Pass candidates to per-auth plugin model resolution |
| `internal/util/gemini_schema.go` | Preserve explicit all-false unions and scope union traversal to schema positions until the generic upstream fix lands |
| `internal/runtime/executor/antigravity_executor_request.go`, `antigravity_executor_tokens.go` | Reject unsupported false schemas after the final payload rules and before generation/count transport |
| `sdk/api/handlers/openai/openai_responses_websocket_forward.go` | End local interrupt eligibility before publishing a terminal response, and reopen it for the next created response |
| `internal/pluginhost/loader_windows.go` | Wrap the native loader with synchronous calls and route host callbacks through the fork bridge |
| `internal/pluginhost/adapters.go` | Match explicit model-provider identifiers and transport candidate models |
| `internal/pluginhost/rpc_schema.go` | Transport host features and model-provider identifiers over RPC |
| `internal/pluginhost/rpc_client.go` | Advertise native-candidate support and preserve model-provider identifiers |
| `sdk/pluginapi/types.go` | Candidate-model and provider-identifier API fields |
| `.github/workflows/{agents-md-guard,pr-path-guard,auto-retarget-main-pr-to-dev,docker-image}.yml` | Exclude `hirsaeki/CLIProxyAPI` from upstream-only contribution policies and Docker Hub publication |

Adding another upstream-owned integration file requires documenting why an
existing seam cannot support the feature.

Catalog refresh publication now lives in `catalog_sources.go`; the startup loader
in `model_updater.go` is not used for remote, configured local, or embedded-source
refreshes. Keep the overlay hook in both loaders, after validation and before
publication. Do not restore the obsolete updater implementation to retain the hook.

## Fork Branch and Workflow Policy

`main` is the integration and release branch for this fork. Open maintenance and
feature pull requests against `main`; do not automatically retarget them to `dev`.
The existing `dev` branch is retained for historical work, not as a required
promotion stage. Do not merge its unrelated history into `main` to land a fix.

The inherited Docker Hub publisher, AGENTS.md auto-close rule, translator-path
rejection rule, and main-to-dev retarget rule do not run jobs in
`hirsaeki/CLIProxyAPI`. Keep their files and job identifiers with explicit fork
exclusions to minimize upstream merge conflicts and preserve historical check
names. These exclusions do not change upstream behavior in other repositories.
They are not a replacement for reviewing sensitive source or instruction changes.

Keep the four-hour upstream sync, fork GHCR publisher, release builds (including
plugins and the optional OAuth helper), WinGet validation, and on-demand repository
bundle. WinGet publication waits for build validation, not the upstream
translator contribution policy. Preserve its version guard before generation,
generator-level downgrade protection, pre-merge version recheck, and serialized
publication job.

Review workflow changes during every upstream merge. Retain these exclusions and
check for newly introduced upstream workflows before enabling them in the fork.
Deleting inherited files instead can create modify/delete conflicts on a later
upstream change. Repository-level Actions enablement is a separate owner action;
committing these policies does not re-enable Actions.

For changes confined to the three repository-local WinGet manifests, PR CI
validates the manifests with `winget validate` and does not compile Go binaries or
Windows plugins. The classifier uses the actual base-to-head PR diff, not the
branch name; mixed changes, renamed source files, and unknown paths retain the
full build path. The historical `build` and `build Windows plugin (amd64/arm64)`
check names remain registered for release workflows stored in older tags. In the
manifest-only path the amd64 job performs validation, and the other two names are
lightweight Linux checks. A classification error fails those checks.

Jobs have explicit time bounds: PR builds 15 minutes, sync 10 minutes, release
builds 15-30 minutes, and WinGet publication 30 minutes with a 20-minute check
wait. FreeBSD package installation is limited to 5 minutes. PR concurrency can
cancel superseded verification; release concurrency only serializes the same
tag and does not cancel another version's publication. Keep network dependency
steps bounded so a stalled mirror cannot consume a runner for the six-hour
GitHub default.

## Model Registration Contract

The model registration order is intentional:

1. Resolve the provider's native catalog and configuration overrides.
2. Apply the authoritative per-credential OAuth availability snapshot.
3. Apply excluded-model rules.
4. Allow a matching plugin to filter or enrich the native candidates.
5. Apply OAuth model aliases and model prefixes.
6. Do not append static plugin models when an authoritative OAuth availability
   entry exists.

Keep the caller's immutable configuration snapshot through plugin discovery,
including native candidates and cached Antigravity catalogs. The per-auth
`WithConfig` registration path must not reacquire current configuration for
exclusions, aliases, OAuth settings, or forced prefixes midway through a batch.
`model_registration_config_snapshot_fork_test.go` covers these integration seams
with different captured and current configurations. Upstream's global-exclusion
fence must still reject obsolete exclusions; captured aliases, settings and
prefixes remain consistent when the exclusion policy is current.

Keep the entry-captured publication fence through all three plugin paths and the
cached Antigravity fallback. Do not reacquire the registry epoch after runtime
reads or plugin discovery. A plugin-returned auth update may refresh the fence's
auth inputs only after the upstream conditional identity update succeeds; its
original registry epoch must survive. `model_registration_publication_fork_test.go`
covers newer publications, handled-empty results, cached fallback, re-registered
auth identities, and publication from an auth-update hook without sleeps.
Propagate upstream's handled/publication result through plugin-only, native and
cached Antigravity discovery. A rejected result must reach the completion pass,
which recomputes once from the latest auth instead of publishing stale models or
leaving a client empty after an in-flight plugin save. Static plugin models remain
subject to upstream exclusions and the authoritative OAuth availability guard.

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

The owner can also open a new Issue in `hirsaeki/CLIProxyAPI` with the exact,
case-sensitive title `[sync-upstream]` to request the same sync. Only Issues
authored and opened by `hirsaeki` are accepted; edits, reopened Issues, comments,
and other users cannot request a sync. The body is ignored, including any refs,
tags, repository names, or commands. Issue-triggered sync checks out trusted
`main` and uses the existing upstream detection and release behavior. Rerunning
an Issue-triggered job also requires `hirsaeki` as the triggering actor.

The read-only Issue authorization job has no checkout or sync token. Only an
authorized sync enters the shared concurrency group, so rejected Issues cannot
replace a pending scheduled, manual, or owner-requested sync. The four-hour cron
and manual dispatch remain available; this alternate entry point does not fix
or diagnose missed scheduled runs. Validate the gate without opening a real
trigger Issue with `python3 scripts/tests/fork-sync-issue_test.py`.

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

Home rebalance commands use a dedicated Redis client so cancellation cannot
interrupt issued dispatches. go-redis pool shutdown can probe an active socket
before closing it and block behind its pending read on Linux. The narrow
`cluster_balance.go` seam closes only that command's sockets before closing its
pool, using `rebalance_transport_cancellation_fork.go` to fence late dial
completion and serialize shutdown. It binds detached Redis dial contexts to the
command lifetime and reuses Home's context-aware TLS dialer and
raw-socket closer. No post-connection deadline is added. This is
a generic upstream correction candidate; remove the seam/helper when upstream
provides equivalent prompt cancellation. Retain the blocked-RPC heartbeat tests
and the fork regressions for interrupted, concurrent, late-dial and TLS shutdown.

The highest-value upstream contribution is a generic per-auth model candidate
resolver seam. It should let a model provider receive the native candidates and
return a filtered or enriched list without requiring provider-specific logic in
`Service.registerModelsForAuthWithCache`.

Upstream now records the source hash of the bytes actually parsed by the config
loader and serializes watcher reloads. This supersedes the fork's temporary
observed-hash acknowledgment fix, so `internal/watcher/config_reload.go` has no
remaining fork production delta. Keep the deterministic callback-write regression
in `internal/watcher/config_reload_hash_fork_test.go` alongside upstream's
management-save, hash/load ordering, and concurrent-reload tests. The runtime-only
config source hash is separate from the fork's auth-file snapshot provenance;
both protections must survive future syncs.

The loader's bcrypt and legacy-layout writebacks can change the file before the
management handler receives the loaded snapshot. Retaining only the pre-write
hash makes the handler reject that snapshot; a management save before the next
reload can then overwrite the external edit with old state. The loader advances
provenance only to its exact successful transformation of the originally parsed
bytes. It never acknowledges an arbitrary later read, and layout cleanup advances
provenance only when its input matches those represented bytes. Keep the
normalization/publication and stale-rejection tests in
`config_source_rewrite_fork_test.go`. This generic loader correction is an upstream
contribution candidate; remove the production delta when upstream adopts it.

The AI Studio thinking-level and Antigravity Interactions executor tests require
known level-capable models, even after those model IDs leave the rolling catalog.
Their only local changes register deterministic capability fixtures through
`internal/runtime/executor/thinking_level_fixture_fork_test.go`. These generic
upstream test corrections preserve every assertion and avoid changing production
thinking behavior, model catalogs, or local overlays. Remove the helper and its
two call sites when upstream makes the tests independent of catalog retirement.

The Vertex synthesized-auth model test writes its source fixture into a temporary
file before registration. The fork's new-registration provenance fence must be
able to validate the same bytes supplied to the synthesizer. Keep the production
fence and all alias assertions; this test-only fixture correction can be removed
when upstream adopts it.

The Git-store corruption fixture closes its repack repository before deleting
packfiles and verifies corruption through a newly opened repository. go-git's
pack-handle cache can otherwise keep an unlinked pack readable. This test-only
compatibility correction preserves every fail-closed recovery and file-preservation
assertion after the go-git dependency update; remove it when upstream adopts it.

The WebSocket retry-bind test assigns server connection ordinals before sending
the upgrade handshake. This prevents a fast retry from being mistaken for the
connection the fixture intentionally closes. The test-only correction retains
all lifecycle and connection-count assertions and changes no executor behavior.

Local HTTP interrupt eligibility must end before a terminal response becomes
visible to a WebSocket client, rather than waiting for the upstream data channel
to close. A later `response.created` reopens eligibility for the next turn.
This temporary generic fix belongs in the response forwarder because it owns
publication ordering; service and plugin registration seams cannot enforce it.
`openai_responses_interrupt_lifecycle_fork_test.go` verifies completed, done, and
incomplete events while both source channels remain open, including next-turn
reactivation. Remove the local production delta when upstream adopts the fix.

The upstream contribution-policy workflows could support a configurable opt-out
for maintained forks. Until then, keep the repository-specific job exclusions
small. The existing same-repository `automation/winget-*` exemption inside the
retarget script remains for compatibility outside this fork.

Until such a hook is accepted upstream:

- keep `model_registration_extensions.go` as the fork-owned policy boundary;
- keep call-site changes in upstream-owned service files minimal;
- avoid adding sidecar knowledge to `internal/pluginhost` or provider executors;
- update this section when a recurring patch can be removed or submitted
  upstream.

If upstream accepts an equivalent hook, migrate the fork implementation to it,
remove redundant call-site patches, retain the behavior tests, and update the
ownership table in the same change.


### Explicit all-false schema unions

The upstream union cleaner can erase `anyOf: [false]` or `oneOf: [false]`,
turning an unsatisfiable schema into a permissive one. There is no extension hook
inside that cleaner, so the fork keeps a narrow preservation guard and schema-only
union traversal in the existing function. Supporting logic and regressions live in
`gemini_schema_false_fork.go` and its test file.

Preserve the original union object, including either all-false sibling, so JSON
Schema carriers and the tool-search object-root path retain its semantics.
Transport representability does not guarantee that every upstream model accepts
an impossible schema. Antigravity's compatibility path conservatively rejects
false schemas locally with HTTP 400 rather than broadening them or sending an
unsupported native Schema value. Its validation runs after payload rules, does
not rewrite their output, and covers generation, streaming/rebuilt requests and
counting. JSON-shaped conversation data and native response
`additionalProperties: false` remain unaffected. This is an upstream contribution
candidate; no general schema satisfiability solver is introduced. The existing
approximate `allOf`/conditional reductions are outside this narrow union-rewrite
fix and may still simplify constraints before union cleaning.

The inherited HTTP-interrupt test now waits for its fixture's cancellation
observer through a channel before retaining its original atomic assertion. The
incomplete WebSocket frame can arrive before that observer runs; missing
cancellation still fails under a bounded timeout. CI repeats this unchanged
production behavior twenty times with the race detector.

### Persisted auth and queued file snapshots

An unversioned file snapshot can be consumed after a newer management save but
before its post-persist hook. Previously the old snapshot replaced the manager
and advanced its generation, causing that hook to reject the saved state.
`persisted_auth_ordering_fork_test.go` reproduces the completed ordering without
sleeps; the original concurrent Issue6559 assertions remain unchanged.

Upstream now owns canonical file-snapshot provenance, synthesis binding, watcher
equality, and replacement-update validation. Use that single mechanism rather
than retaining the fork's raw-byte marker: formatting-only rewrites must not
create spurious updates or reject current snapshots. The fork's
`SetSourceFileSnapshot` API delegates to upstream `BindFileSnapshot` for compatibility.
The redundant `auth/types.go` and synthesizer production deltas are removed.

Upstream does not validate new registrations before committing. Keep the narrow
`Manager.Register` guard under the existing per-auth mutation gate, before
generation/registration-epoch changes or persistence. It uses upstream
`FileSnapshotMatchesDisk` and preserves the current auth (or absence) when a
source is changed, missing or unreadable. Both new and existing registrations
accept formatting-only rewrites. Runtime commits clear the observation marker.

Service model registration uses the manager's returned committed/current snapshot,
including rejected updates. Executor binding occurs only after reconciliation,
so rejected plugin snapshots cannot remove a current compatibility executor.
Stale-generation inputs return before executor side effects. Keep this ordering
when adopting upstream's same-generation early return and completion retry.

No source bytes or digest are logged or serialized. File I/O stays outside the
manager mutex, and plugin parsing never runs inside the mutation gate. The guard
serializes manager-mediated changes for the same auth; it does not lock out
arbitrary external file writers. Regressions cover real save ordering, accounting,
clone/serialization behavior, canonical formatting equivalence, and
full-scan/single-file/plugin synthesis paths. Retire the remaining Register seam
when upstream adds equivalent registration validation.

## Claude SSE event bounds

The scanner's per-line limit does not bound an event containing many short lines.
Keep the 50 MiB whole-event limit in `helps/claude_sse_limit_fork.go`, matching the
existing scanner ceiling. Native and translated executor paths must reject before
appending, report failed usage, close the body, and skip successful continuity
publication. The handler observer uses a single bounded data buffer plus a bounded
partial line; per-line slices would retain unbounded allocation overhead. Completed
line/event buffers above 64 KiB are released rather than retained for a long-lived
stream; small buffers remain reusable. Its
fork-owned helper rejects the first chunk before committing headers and later
chunks through the existing `ChunkError` hook. A withheld chunk must not advance
written-newline or terminal state. Overflow emits one sanitized error, never a
synthetic successful terminator. These narrow upstream-file seams are needed
because the existing scanner and forwarder hooks alone do not bound parsing state.

`TestClaudeExecutorSSEEventLimitFork` exercises native and translated overflow while
the upstream stays open, including body closure and failed token accounting. The
fork-owned helper/handler tests cover exact limits, resets, many short lines,
unterminated lines, first-chunk handling, and single-error cancellation. Keep these
alongside the upstream multiline, truncation, and terminal-state regressions.
