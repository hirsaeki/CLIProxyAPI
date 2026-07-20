package main

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type matrixFetcher func(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error)

type matrixCache struct {
	mu           sync.Mutex
	fetch        matrixFetcher
	now          func() time.Time
	sourceURL    string
	matrix       locationMatrix
	expiresAt    time.Time
	etag         string
	lastModified string
}

func newMatrixCache(fetch matrixFetcher) *matrixCache {
	return &matrixCache{fetch: fetch, now: time.Now}
}

func (c *matrixCache) reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.sourceURL = ""
	c.matrix = nil
	c.expiresAt = time.Time{}
	c.etag = ""
	c.lastModified = ""
	c.mu.Unlock()
}

func (c *matrixCache) get(callbackID string, cfg pluginConfig) (locationMatrix, error) {
	if c == nil || c.fetch == nil {
		return nil, fmt.Errorf("location matrix fetcher is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.sourceURL != cfg.DocsURL {
		c.sourceURL = cfg.DocsURL
		c.matrix = nil
		c.expiresAt = time.Time{}
		c.etag = ""
		c.lastModified = ""
	}
	if c.matrix != nil && now.Before(c.expiresAt) {
		return c.matrix, nil
	}

	headers := make(http.Header)
	if c.etag != "" {
		headers.Set("If-None-Match", c.etag)
	}
	if c.lastModified != "" {
		headers.Set("If-Modified-Since", c.lastModified)
	}
	resp, errFetch := c.fetch(callbackID, cfg.DocsURL, headers)
	if errFetch != nil {
		return c.staleOrError(fmt.Errorf("fetch location matrix: %w", errFetch), now, cfg.CacheTTL)
	}
	if resp.StatusCode == http.StatusNotModified {
		if c.matrix == nil {
			return nil, fmt.Errorf("location matrix returned 304 without a cached matrix")
		}
		c.expiresAt = now.Add(cfg.CacheTTL)
		return c.matrix, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return c.staleOrError(fmt.Errorf("location matrix returned HTTP %d", resp.StatusCode), now, cfg.CacheTTL)
	}
	matrix, errParse := parseLocationMatrix(bytes.NewReader(resp.Body))
	if errParse != nil {
		return c.staleOrError(errParse, now, cfg.CacheTTL)
	}
	c.matrix = matrix
	c.expiresAt = now.Add(cfg.CacheTTL)
	c.etag = resp.Headers.Get("Etag")
	c.lastModified = resp.Headers.Get("Last-Modified")
	return c.matrix, nil
}

func (c *matrixCache) staleOrError(err error, now time.Time, ttl time.Duration) (locationMatrix, error) {
	if c.matrix != nil {
		retryDelay := ttl
		if retryDelay <= 0 || retryDelay > 5*time.Minute {
			retryDelay = 5 * time.Minute
		}
		c.expiresAt = now.Add(retryDelay)
		return c.matrix, nil
	}
	return nil, err
}
