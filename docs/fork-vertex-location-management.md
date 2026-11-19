# Editing a Vertex service account's location

Use the existing v8 management API to update the location of a saved, file-backed Vertex service-account credential:

```http
PATCH /v8/management/credentials/fields
Content-Type: application/json

{"name":"vertex-example.json","location":"eu"}
```

This is a credential-level setting, not a plugin-wide setting. The save persists the updated credential and the runtime post-persist hook re-registers its models. Subsequent Vertex GenerateContent, streaming GenerateContent, and CountTokens requests use the new location; in-flight requests are not migrated. The Vertex Region Models plugin reads the same credential location to resolve model availability.

Use an explicit location: `global`, `us`, `eu`, or a supported region such as `europe-west4`. A blank or missing location in an existing auth file means `global` at runtime; this is separate from the default used when importing a service account. Changing a region does not override model availability or permissions in that region.

This edit is not for Gemini API keys or Vertex API-key credentials. It does not change native Vertex Interactions endpoint behavior. The Management Web form is maintained separately in the [Management Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) repository.
