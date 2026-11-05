package util

// The envelope encoder copies into a fresh allocation before publishing it.
// Keep the normal source scanner and exact write-count checks unchanged.
func init() {
	reviewedInPlaceByteWrites["internal/kvstore/bolt_store.go"] = reviewedInPlaceByteWrite{
		count:  1,
		reason: "encodeEnvelope copies caller bytes into a freshly allocated binary envelope before returning it; the caller's buffer is never modified or reused",
	}
}
