package management

// Credential verification accepts small control metadata, not credential uploads.
// Bound it independently of the optional server-wide request body limit.
const maxAuthVerifyBodyBytesFork = 1 << 20
