package cache

import (
	"sync"
	"time"
)

const (
	// InvalidSignatureCacheTTL is 3 hours as per plan
	InvalidSignatureCacheTTL = 3 * time.Hour
	// InvalidSignatureCleanupInterval controls background purge
	InvalidSignatureCleanupInterval = 10 * time.Minute
)

// invalidSignatureCache stores sessionID -> signature -> timestamp
var invalidSignatureCache sync.Map
var invalidCleanupOnce sync.Once

type sessionInvalidCache struct {
	mu      sync.RWMutex
	entries map[string]time.Time
}

func getOrCreateInvalidSession(sessionID string) *sessionInvalidCache {
	invalidCleanupOnce.Do(startInvalidSignatureCleanup)

	if val, ok := invalidSignatureCache.Load(sessionID); ok {
		return val.(*sessionInvalidCache)
	}
	sc := &sessionInvalidCache{entries: make(map[string]time.Time)}
	actual, _ := invalidSignatureCache.LoadOrStore(sessionID, sc)
	return actual.(*sessionInvalidCache)
}

func startInvalidSignatureCleanup() {
	go func() {
		ticker := time.NewTicker(InvalidSignatureCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			purgeExpiredInvalidSignatures()
		}
	}()
}

func purgeExpiredInvalidSignatures() {
	now := time.Now()
	invalidSignatureCache.Range(func(key, value any) bool {
		sc := value.(*sessionInvalidCache)
		sc.mu.Lock()
		for sig, ts := range sc.entries {
			if now.Sub(ts) > InvalidSignatureCacheTTL {
				delete(sc.entries, sig)
			}
		}
		isEmpty := len(sc.entries) == 0
		sc.mu.Unlock()
		if isEmpty {
			invalidSignatureCache.Delete(key)
		}
		return true
	})
}

// CacheInvalidSignature records a signature that was rejected by the upstream.
func CacheInvalidSignature(sessionID, signature string) {
	if sessionID == "" || signature == "" {
		return
	}
	sc := getOrCreateInvalidSession(sessionID)
	sc.mu.Lock()
	sc.entries[signature] = time.Now()
	sc.mu.Unlock()
}

// IsInvalidSignature returns true if the signature is blacklisted for this session.
func IsInvalidSignature(sessionID, signature string) bool {
	if sessionID == "" || signature == "" {
		return false
	}
	val, ok := invalidSignatureCache.Load(sessionID)
	if !ok {
		return false
	}
	sc := val.(*sessionInvalidCache)
	sc.mu.RLock()
	ts, exists := sc.entries[signature]
	sc.mu.RUnlock()
	return exists && time.Since(ts) <= InvalidSignatureCacheTTL
}

// ClearInvalidSignatureCache clears blacklist for session or all.
func ClearInvalidSignatureCache(sessionID string) {
	if sessionID != "" {
		invalidSignatureCache.Delete(sessionID)
	} else {
		invalidSignatureCache.Range(func(key, _ any) bool {
			invalidSignatureCache.Delete(key)
			return true
		})
	}
}
