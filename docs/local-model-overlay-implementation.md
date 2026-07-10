# Local Model Overlay Implementation

## Purpose

This local-only escape hatch lets operators add or override Google-family model
metadata while the upstream remote model catalog catches up. It is intentionally
kept out of the public config schema, management APIs, and sample config files so
the patch can be removed cleanly when it is no longer needed.

## Implementation Diff

- `internal/registry/model_overlay.go` adds the overlay loader and merger.
- `internal/registry/model_updater.go` applies the overlay after embedded catalog
  loading and after each successful remote refresh, before change detection and
  storage.
- `internal/registry/model_overlay_test.go` covers embedded loading, remote
  refresh preservation, unchanged refresh notifications, invalid overlay fallback,
  and unsupported provider sections.

No existing documentation, `config.example.yaml`, management handlers, or SDK
configuration types are changed.

## Activation

Set `CLIPROXY_LOCAL_MODELS_JSON` to a JSON file path before starting the server.

```bash
CLIPROXY_LOCAL_MODELS_JSON=/path/to/local-models.json ./cli-proxy-api
```

If the variable is unset, the registry behaves exactly as before. If the file is
missing or invalid, startup and refresh continue with the embedded or remote
catalog and a warning is logged.

## Overlay Format

The overlay uses the same top-level shape as `internal/registry/models/models.json`,
but only these sections are merged:

- `gemini`
- `vertex`
- `aistudio`

Other sections are ignored by the overlay merger. Each listed model must include
a non-empty `id`; other metadata follows `registry.ModelInfo`.

```json
{
  "gemini": [
    {
      "id": "gemini-new-preview",
      "object": "model",
      "created": 1760000000,
      "owned_by": "google",
      "type": "gemini",
      "display_name": "Gemini New Preview",
      "name": "models/gemini-new-preview",
      "inputTokenLimit": 1048576,
      "outputTokenLimit": 65536,
      "supportedGenerationMethods": ["generateContent", "countTokens"],
      "thinking": {
        "min": 128,
        "max": 32768,
        "dynamic_allowed": true,
        "levels": ["minimal", "low", "medium", "high"]
      }
    }
  ],
  "vertex": [
    {
      "id": "gemini-new-preview",
      "object": "model",
      "owned_by": "google",
      "type": "gemini",
      "name": "models/gemini-new-preview"
    }
  ],
  "aistudio": [
    {
      "id": "gemini-new-preview",
      "object": "model",
      "owned_by": "google",
      "type": "gemini",
      "name": "models/gemini-new-preview"
    }
  ]
}
```

## Merge Order

1. Load the embedded or remote model catalog.
2. Read `CLIPROXY_LOCAL_MODELS_JSON` if set.
3. Validate overlay model IDs for the supported sections.
4. Upsert overlay models into `gemini`, `vertex`, and `aistudio` by case-insensitive
   trimmed model `id`.
5. Validate the merged catalog.
6. Store the merged catalog and run refresh change detection against the merged
   result.

Overlay entries replace catalog entries with the same `id`. New IDs are appended.
Remote catalog refresh remains enabled and continues to update every section; the
local overlay is re-applied after every successful refresh.

## Removal

To stop using the local override at runtime, unset `CLIPROXY_LOCAL_MODELS_JSON`
and restart the process.

To remove the implementation patch, delete:

- `internal/registry/model_overlay.go`
- `internal/registry/model_overlay_test.go`
- the overlay calls in `internal/registry/model_updater.go`
- this document

## Limitations

- The overlay can advertise a model that the selected upstream credential does
  not actually support. The upstream request will still fail in that case.
- Incorrect `thinking` metadata can change thinking-budget normalization.
- The overlay is process-local and is not visible through management config APIs.
- The overlay file is read during catalog load and refresh; it is not watched
  independently.
