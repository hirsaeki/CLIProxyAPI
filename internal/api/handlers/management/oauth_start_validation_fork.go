package management

// OAuth-start metadata is a small control request, not a credential upload.
// Bound it even when the server-wide optional request-body limit is disabled.
const maxOAuthStartBodyBytesFork = 1 << 20
