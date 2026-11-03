# Vertex Region Models

`vertex-region-models` binds to the native `vertex` auth provider without replacing
its executor. Google's location matrix and the last successfully parsed cache are
authoritative for model IDs. Native candidates only contribute metadata when an
exact or safe `-preview` counterpart exists.

Windows releases publish this plugin separately from the server archives. The
plugin ZIPs preserve `plugins/windows/<arch>` and use the stable plugin ID
`vertex-region-models`. Extract the matching ZIP into `~/.cli-proxy-api` and
configure `dir: "~/.cli-proxy-api/plugins"`. The plugin remains opt-in.

See [vertex-region-models/README.md](vertex-region-models/README.md) for configuration, cache, and failure behavior.
