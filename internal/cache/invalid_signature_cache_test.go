package cache

import (
	"testing"
)

func TestInvalidSignatureCache(t *testing.T) {
	sessionID := "test-session"
	sig := "test-sig"

	// Initial check
	if IsInvalidSignature(sessionID, sig) {
		t.Error("Signature should not be invalid initially")
	}

	// Cache it
	CacheInvalidSignature(sessionID, sig)

	// Verify it's invalid
	if !IsInvalidSignature(sessionID, sig) {
		t.Error("Signature should be invalid after caching")
	}

	// Verify other session is not affected
	if IsInvalidSignature("other-session", sig) {
		t.Error("Other session should not have the signature cached")
	}

	// Clear it
	ClearInvalidSignatureCache(sessionID)
	if IsInvalidSignature(sessionID, sig) {
		t.Error("Signature should be valid after clear")
	}
}

func TestInvalidSignatureCacheTTL(t *testing.T) {
	// This tests the logic, but we won't wait 3 hours.
	// We can manually manipulate the entries if we wanted, but let's just
	// verify the purge logic doesn't crash.
	CacheInvalidSignature("ttl-session", "sig1")
	purgeExpiredInvalidSignatures()
	if !IsInvalidSignature("ttl-session", "sig1") {
		t.Error("Signature should still be valid immediately after purge")
	}
}
