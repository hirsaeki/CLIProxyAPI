# OAuth Model Availability Sidecar

CLIProxyAPI can use a startup-only model availability sidecar for individual
Claude and xAI OAuth credentials. The sidecar is optional. It lets the official
provider clients determine which model IDs a credential may use without making
Models API calls from the proxy runtime.

This mechanism is separate from the Vertex region model plugin. Vertex needs a
location-specific documentation matrix; Claude and xAI use a local one-shot
helper and do not share plugin lifecycle or cache state with Vertex.

## Quick Start

Download and extract the CLIProxyAPI release archive for your platform. It
contains both `cli-proxy-api` and `sync-oauth-model-availability`, plus this
document under `docs/`. Windows uses the `.exe` suffix. WinGet installations
register both executable names as command aliases.

The commands below assume a Unix-like shell in the extracted archive directory.
On Windows, use the corresponding `.exe` name; omit `./` when using a WinGet
command alias.

### 1. Prepare both login states

The same account must be logged in to CLIProxyAPI and the official provider
client.

Create the CLIProxyAPI OAuth credential if it does not already exist:

```bash
./cli-proxy-api --config ./config.yaml --claude-login
```

or:

```bash
./cli-proxy-api --config ./config.yaml --xai-login
```

Also log in with Claude Code for Claude collection, or with the official Grok
Build client for xAI collection. The helper refuses to write the sidecar when
it cannot verify that the official client account matches the selected
CLIProxyAPI credential.

Claude collection requires `node` and `pnpm`. xAI collection requires `cargo`
and `git` when the helper installs Grok Build automatically. You can avoid the
xAI build step by supplying an existing official executable with
`--grok-binary`.

### 2. Find the CLIProxyAPI auth ID

Look in the `auth-dir` configured by `config.yaml`. The auth ID is the JSON
file's path relative to `auth-dir`; for the usual flat layout it is simply the
filename, including `.json`.

For example:

```text
auth-dir entry: auths/claude-user@example.com.json
auth ID:         claude-user@example.com.json
```

Do not use the email label alone if it differs from the filename. On Windows,
file-backed auth IDs are normalized to lowercase.

### 3. Generate the sidecar before enabling it

For Claude:

```bash
./sync-oauth-model-availability \
  --provider claude \
  --auth-id claude-user@example.com.json \
  --config ./config.yaml \
  --output ./oauth-model-availability.json
```

For xAI, allowing the helper to install the latest official client into its
user cache:

```bash
./sync-oauth-model-availability \
  --provider xai \
  --auth-id xai-user.json \
  --config ./config.yaml \
  --output ./oauth-model-availability.json
```

With an existing official Grok Build executable:

```bash
./sync-oauth-model-availability \
  --provider xai \
  --auth-id xai-user.json \
  --config ./config.yaml \
  --output ./oauth-model-availability.json \
  --grok-binary /path/to/grok
```

Run the command once for every Claude or xAI OAuth credential that needs an
authoritative list. Each run preserves entries generated for other
credentials.

### 4. Enable the completed sidecar

Only after the output file exists, add this to `config.yaml`:

```yaml
oauth-model-availability-file: ./oauth-model-availability.json
```

Restart CLIProxyAPI. A successful startup logs that the sidecar was loaded and
applied. A hot reload does not load a newly generated file; restart again after
updating it.

If a credential is intentionally omitted from the sidecar, its startup warning
is expected and that credential continues to use the central catalog.

## Behavior

Configure the sidecar path in `config.yaml`:

```yaml
oauth-model-availability-file: ./oauth-model-availability.json
```

Relative paths are resolved from the directory containing the CLIProxyAPI
configuration file.

- The file is read and validated once while the service is built.
- A configured file that is missing, malformed, or invalid prevents service
  construction. The error is logged.
- Changing the configured path during a hot reload logs a warning. Restart the
  service to load the new file.
- A matching `(provider, auth_id)` entry is authoritative for that Claude or
  xAI OAuth credential. Exact IDs already present in the central catalog retain
  their complete native metadata.
- IDs reported only by the official client receive conservative metadata from
  the sidecar. Unsupported capabilities are not inferred.
- If an OAuth credential has no matching sidecar entry, CLIProxyAPI logs a
  warning and uses the existing central catalog.
- API-key credentials are unaffected.
- Excluded-model rules run after the sidecar allowlist. OAuth aliases and model
  prefixes run afterward.
- Central catalog refreshes re-register credentials against the already loaded
  sidecar snapshot. The file is not reread.

The runtime does not call a provider Models API because this setting is enabled.

## One-shot sync helper

Run the helper separately for each credential, using the exact auth ID shown by
CLIProxyAPI:

```bash
./sync-oauth-model-availability \
  --provider claude \
  --auth-id claude-user@example.com.json \
  --config ./config.yaml \
  --output ./oauth-model-availability.json
```

```bash
./sync-oauth-model-availability \
  --provider xai \
  --auth-id xai-user.json \
  --config ./config.yaml \
  --output ./oauth-model-availability.json
```

The helper updates only the selected `(provider, auth_id)` entry, preserves all
other entries, and replaces the output atomically with mode `0600`. A failed
client run, empty model result, stale or corrupt xAI cache, identity mismatch,
or unverifiable identity leaves the existing sidecar unchanged.

### Claude collection

The helper uses `pnpm` to install the current
`@anthropic-ai/claude-agent-sdk` release into the user cache under
`cli-proxy-api/oauth-model-availability/claude`. It starts an SDK session with
an empty prompt stream and calls only:

- `initializationResult()` for account and backend information;
- `supportedModels()` for the official client model list.

It requires Anthropic first-party OAuth and compares the SDK account email with
the requested CLIProxyAPI credential. It does not submit an inference prompt.

### xAI collection

By default, the helper uses `cargo install` to build the latest official
`xai-org/grok-build` source into the user cache under
`cli-proxy-api/oauth-model-availability/grok`. It then runs:

```text
grok models
```

and reads the official `~/.grok/models_cache.json`. The cache must be a recent
session-auth result. The helper compares the identity in official
`~/.grok/auth.json` with the requested CLIProxyAPI credential. It parses these
files through narrow structures and never copies credentials into the sidecar.
If the official auth store contains multiple distinct OAuth identities, the
helper refuses the update because it cannot prove which identity fetched the
cache.

To use an already installed official client instead of building one, pass:

```bash
--grok-binary /path/to/grok
```

Use `--grok-home` only when the official client itself uses a non-default state
directory.

`grok models` performs the model-list operation implemented by the official xAI
client. The helper does not send an inference request.

## Schema

The current schema version is `1`:

```json
{
  "schema_version": 1,
  "generated_at": "2026-07-25T00:00:00Z",
  "credentials": [
    {
      "provider": "claude",
      "auth_id": "claude-user@example.com.json",
      "client": {
        "name": "@anthropic-ai/claude-agent-sdk",
        "version": "0.3.219",
        "artifact_sha256": "..."
      },
      "models": [
        {
          "id": "claude-sonnet-5",
          "display_name": "Claude Sonnet 5",
          "description": "...",
          "supported_effort_levels": ["low", "medium", "high"]
        }
      ]
    }
  ]
}
```

Only `claude` and `xai` are accepted. Provider matching is case-insensitive;
auth IDs are exact. Credentials and model IDs must be non-empty and unique
within their scopes, and every credential entry must contain at least one
model.

Optional model fields are:

- `display_name`
- `description`
- `context_length`
- `max_completion_tokens`
- `supported_parameters`
- `supported_input_modalities`
- `supported_output_modalities`
- `supported_effort_levels`

## Logging and privacy

At info level, the runtime and helper log provider/auth ID, counts, client
version, and duration summaries. Debug logging adds model ID differences. Warn
logging reports missing credential entries, sidecars older than 24 hours, and
restart-required configuration changes.

Tokens, authorization headers, emails, account identifiers, and raw official
client output are not logged. The sidecar contains model availability and
client provenance only; it must not contain credentials.
