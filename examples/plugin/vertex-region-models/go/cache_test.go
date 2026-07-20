package main

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestMatrixCacheUsesTTLAndConditionalRequest(t *testing.T) {
	now := time.Unix(1000, 0)
	var calls int
	fetch := func(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error) {
		calls++
		if callbackID != "callback-1" || rawURL != "https://docs.example/locations" {
			t.Fatalf("fetch request = callback=%q url=%q", callbackID, rawURL)
		}
		if calls == 1 {
			return pluginapi.HTTPResponse{
				StatusCode: http.StatusOK,
				Headers: http.Header{
					"Etag":          []string{`"matrix-v1"`},
					"Last-Modified": []string{"Mon, 20 Jul 2026 00:00:00 GMT"},
				},
				Body: []byte(matrixFixture),
			}, nil
		}
		if headers.Get("If-None-Match") != `"matrix-v1"` || headers.Get("If-Modified-Since") == "" {
			t.Fatalf("conditional headers = %#v", headers)
		}
		return pluginapi.HTTPResponse{StatusCode: http.StatusNotModified}, nil
	}
	cache := newMatrixCache(fetch)
	cache.now = func() time.Time { return now }
	cfg := pluginConfig{DocsURL: "https://docs.example/locations", CacheTTL: time.Hour, FailOpen: true}

	first, errFirst := cache.get("callback-1", cfg)
	if errFirst != nil || len(first["global"]) == 0 {
		t.Fatalf("first cache get = %#v, %v", first, errFirst)
	}
	if _, errCached := cache.get("callback-1", cfg); errCached != nil {
		t.Fatalf("cached get error = %v", errCached)
	}
	if calls != 1 {
		t.Fatalf("fetch calls before expiry = %d, want 1", calls)
	}

	now = now.Add(2 * time.Hour)
	second, errSecond := cache.get("callback-1", cfg)
	if errSecond != nil || len(second["global"]) == 0 {
		t.Fatalf("304 cache get = %#v, %v", second, errSecond)
	}
	if calls != 2 {
		t.Fatalf("fetch calls after expiry = %d, want 2", calls)
	}
}

func TestMatrixCacheRetainsLastKnownGoodOnRefreshFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	var calls int
	cache := newMatrixCache(func(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error) {
		calls++
		if calls == 1 {
			return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(matrixFixture)}, nil
		}
		return pluginapi.HTTPResponse{}, errors.New("docs unavailable")
	})
	cache.now = func() time.Time { return now }
	cfg := pluginConfig{DocsURL: "https://docs.example/locations", CacheTTL: time.Minute, FailOpen: true}
	if _, errFirst := cache.get("callback", cfg); errFirst != nil {
		t.Fatalf("first get error = %v", errFirst)
	}
	now = now.Add(2 * time.Minute)

	matrix, errStale := cache.get("callback", cfg)
	if errStale != nil || len(matrix["global"]) == 0 {
		t.Fatalf("stale fallback = %#v, %v", matrix, errStale)
	}
	if _, errRetryBackoff := cache.get("callback", cfg); errRetryBackoff != nil {
		t.Fatalf("stale retry backoff error = %v", errRetryBackoff)
	}
	if calls != 2 {
		t.Fatalf("fetch calls during stale retry backoff = %d, want 2", calls)
	}
}

func TestMatrixCacheSerializesConcurrentFetches(t *testing.T) {
	var calls atomic.Int32
	cache := newMatrixCache(func(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error) {
		calls.Add(1)
		return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(matrixFixture)}, nil
	})
	cfg := pluginConfig{DocsURL: "https://docs.example/locations", CacheTTL: time.Hour, FailOpen: true}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, errGet := cache.get("callback", cfg); errGet != nil {
				t.Errorf("cache get error = %v", errGet)
			}
		}()
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent fetch calls = %d, want 1", got)
	}
}
