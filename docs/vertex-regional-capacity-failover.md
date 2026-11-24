# Vertex regional capacity failover

Opt-in for ordinary, text-only Gemini `generateContent` and
`streamGenerateContent` via the native Vertex service-account executor. The same
auth entry, service account, OAuth token, project, and exact resolved upstream
Gemini model are used for every location. No auth files are mutated by a request.

## Configure

For a managed Vertex service-account credential in CPA's local file store, a build that includes
the Phase 1 backend for [#173](https://github.com/hirsaeki/CLIProxyAPI/issues/173)
can manage the policy through its authenticated v8 Management API:

- `GET /v8/management/credentials/vertex-failover?name=vertex-example.json`
- `PATCH /v8/management/credentials/vertex-failover` with
  `{"name":"vertex-example.json","mode":"automatic","revision":"<revision from GET>"}`
  or `"mode":"off"` with the same revision precondition.

Both accept the optional `auth_index` (query or body respectively) to verify
the credential identity; it is required if the name matches multiple records.
The response contains only mode, explicitness and an
opaque `revision`, for example
`{"mode":"automatic","explicit":true,"revision":"<opaque value>"}`.
No credentials or legacy policy contents are returned. On HTTP 409, reload
the current policy before offering to confirm again. Restarting the backend
invalidates outstanding revisions. Older CPA builds lack this route. A Management
Center control requires the separate UI update. Git-, object-, and
Postgres-backed credentials are not editable through this endpoint.

New settings are persisted as `vertex_region_failover_mode`, and successful
changes take effect on subsequent requests without a restart. CPA coordinates
managed policy edits, imports, and v8 upload/delete operations. Disk checks and
atomic replacement are not a cross-process compare-and-swap. Do not edit the
same auth file externally at the same time or share this auth directory between
independent CPA writers. A managed import that replaces the service-account
material does not inherit the previous account’s automatic opt-in. The fixed
primary location is unchanged. When neither mode nor legacy mapping is
present, the default is OFF (`"explicit":false`). Explicit OFF overrides
any legacy mapping. Explicit automatic mode allows **all safe built-in
endpoints** supported by fresh official evidence for the exact upstream
model. The saved primary is first, followed by Global, US, EU, and the
remaining documented groups in stable built-in order, omitting duplicates
and unsupported locations. Do not enable automatic mode if data must stay
in a particular geography: Global is not a data-residency guarantee, and
other regions may process requests.

### Legacy restricted policy

Without an explicit mode, existing per-model lists continue unchanged, and
the API reports `mode: "legacy"` and `explicit: false`. Changing mode never
deletes the stored list. The legacy mapping in the existing credential JSON
is a top-level map from exact upstream **model ID** to an ordered list:

```json
{
  "location": "us",
  "vertex_region_failover": {
    "gemini-4-argon": ["us", "us-central1", "global"]
  }
}
```

The list is permission and priority, not a copy of model availability. Include
only destinations that your data policy allows for this credential and Standard
PayGo model. `global` is not a regional data-residency guarantee. Do not use this
feature to work around project quotas or contractual capacity restrictions.

The first entry must match the saved `location`. Accepted lists have 2–16
distinct, lowercase official locations; the built-in endpoint set covers
`global`, `us`, `eu` and the single-region entries in the Gemini location
tables. Missing or invalid policy entries leave the model on its existing
native behavior. Neither catalog entries nor aliases add new permissions.
The management panel's fixed-location selector is unchanged.

Only Gemini GenerateContent and OpenAI Chat Completions client formats are
eligible. API keys, countTokens, Imagen and other known non-text model
families, Interactions (including translated Interactions), OpenAI Responses,
the Vertex OpenAPI upstream route, non-text requests, calls with tools,
cached content, explicit quota routing or
provisioned-throughput headers, custom targets, and other unfamiliar fields
are outside this mode. They retain existing native behavior. The payload is
checked after CPA's existing translation and user payload configuration.

## Published-location evidence

When the CPA service starts, it schedules a public HTTPS GET of Google's
[official Vertex model/location tables](https://docs.cloud.google.com/gemini-enterprise-agent-platform/resources/locations)
in the background. Startup and generation never wait for the inventory.
One worker per service refreshes every 30 minutes, regardless of how many
credentials are present; executor replacements share the same snapshot.
The fetch uses the service's outbound proxy setting, no Vertex credential,
no generation probe, and no request-controlled URL. The optional plugin
shares the same parser implementation and official default URL, but its
custom URL, compatibility parser and last-good discovery cache are not
trusted for native routing.

An immutable routing snapshot is replaced only by a bounded HTTP 200 response
with the complete first-party Gemini region section and positively marked
exact model/destination pairs. Duplicate, missing or malformed coordinates,
unrecognized section layout, partial responses, and an unsolicited 304 are
not new evidence. Hard freshness is six hours from the last fully verified
response. Repeated failed refreshes **do not** extend it.

CPA keeps the configured primary even when a catalog entry is unknown. For
secondaries, it intersects the selected mode's ordered destinations with
fresh exact-model support, preserving order; it checks the latest evidence again before each
secondary attempt. If no secondary qualifies at entry (including at cold
startup or after expiry), the new router does not take over and the native
primary path is unchanged. Existing CPA retry and configured alternate-auth
behavior remains independent of this credential's regional policy when the native
path owns the request. Evidence is published availability, not project IAM,
entitlement, data residency or live capacity.

Startup/refresh logs report `fresh`, `last_good`, `unavailable`, or
`expired` and, when available, the model count, last verified time and
freshness deadline. Expiry is enforced immediately when routing, including
between refresh ticks.

## Request behavior

1. **Phase 1:** Visit the primary and then every eligible location in order at
   most once, stopping on success or a terminal error. Skip endpoints on
   confirmed regional cooldown. Both opt-in modes advance on an explicit,
   temporary regional-capacity refusal. **Automatic** also advances on the
   exact documented PayGo retry-later 429 envelope (optionally with valid
   RetryInfo). Bare or other 429 text, explicit quota, acceleration or
   provisioned-throughput errors, unknown details, permission errors, and
   model, endpoint or transport errors do not advance it. In legacy mode, an
   exact generic retry-later response from the first normal primary attempt
   still goes to CPA's pre-existing retry and alternate-credential policy;
   after routing takes over, that response is terminal in legacy mode. An
   upstream HTTP 503 received before an accepted response also advances to
   the next verified location in Phase 1. A valid HTTP `Retry-After` applies
   within the same wait budget; when the sweep ends on 503, CPA returns that
   upstream response and does not start Phase 2 or native retries from it.
   A 503 does not write a shared regional cooldown. Local CPA errors, such as
   `auth_unavailable`, are not upstream responses and cannot advance the sweep.
2. **Phase 2:** After fully sweeping the eligible endpoints, make up to
   `min(request-retry, auth.request_retry if set, 4)` additional attempts for
   retryable refusals. This replaces CPA's outer retry and credential
   rotation for the routed attempt. Use capped exponential equal jitter.
   Confirmed regional refusals have a shared per-region minimum cooldown of
   one second, increased to the greater valid HTTP `Retry-After` or Google
   retry hint. A generic PayGo refusal gives that endpoint a one-second
   readiness delay only for this request, and its greater valid
   `Retry-After` or RetryInfo is a floor for **every** later attempt on this
   request, even during Phase 1. If that floor cannot be reached within the
   existing wait budget and caller deadline, return 429 without dispatching
   another attempt. The route may wait only until
   `min(max-retry-interval, 8s)` from entry when both retry settings are
   positive, further bounded by the caller's deadline. A zero retry count
   disables Phase 2 but does not suppress a Phase 1 sweep that requires no
   wait. CPA does not add an HTTP timeout to an accepted response or stream.
3. **Commit:** An HTTP success response ends routing. Streaming errors after
   an upstream 2xx, even before any downstream payload, are not replayed.
   Requests and redirects never leave the approved generated Vertex URLs.

Positive regional capacity cooldowns are shared in memory by project, model,
and location. A short overlapping response cannot replace a longer
`Retry-After`; successful overlapping requests cannot clear it. A single probe
can run after a cooldown expires. This is not a durable, cross-instance
limit. Exhaustion returns 429 without changing generic auth/model quota
state, and a retry hint reflects the earliest approved location's readiness
subject to any remaining request-wide PayGo hint.
Default service logs show `vertex regional HTTP attempt` for actual sends
during failover, with region, phase (1 = sweep; 2 = retry), attempt number and
HTTP status (0 = transport error). The attempt result distinguishes
`upstream_503`, `generic_paygo_429`, and `regional_capacity_429`.
`vertex regional candidate skipped` is not a send.
`vertex regional routing finished` summarizes recovery, exhaustion, legacy
native handoff, a hint beyond the wait budget (`retry_hint_exceeds_budget`),
or why there was no regional attempt (for example, `policy_off`,
`inventory_unavailable` or `no_verified_secondary`). These messages use the
normal request ID when available and do not require debug or request-body
logging. A normal primary success emits none of these diagnostics.

[Google's 429 guidance](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/deploy/error-code-429)
describes shared capacity as well as fixed quota and acceleration 429s. The
generic documented PayGo text is not evidence of live capacity in any
particular region. Automatic treats its narrowly recognized envelope as
permission for a bounded, same-account retry, not as a regional-capacity
diagnosis or a way to work around fixed quota. Synthetic tests cover both
response classes; no live provider routing behavior was verified.
