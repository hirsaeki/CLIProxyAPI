package vertexlocations

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/proxyutil"
)

const (
	refreshIntervalFork = 30 * time.Minute
	freshnessFork       = 6 * time.Hour
	fetchTimeoutFork    = 15 * time.Second
	maxDocumentFork     = 4 << 20
)

type fetchFork func(context.Context) ([]byte, error)

// RefreshStatus reports inventory provenance, never request or credential data.
type RefreshStatus struct {
	State      string
	VerifiedAt time.Time
	FreshUntil time.Time
	ModelCount int
	Err        error
}

// Inventory is a single service-owned catalog. Publish validated snapshots as
// immutable values, never mutable maps reachable by a request.
type Inventory struct {
	current atomic.Pointer[Snapshot]
	running atomic.Bool
}

// Snapshot answers exact upstream model/location membership while fresh.
type Snapshot struct {
	models     modelRegionsFork
	verifiedAt time.Time
	deadline   time.Time
}

// Supports does not normalize, alias or infer availability.
func (s *Snapshot) Supports(model, location string, now time.Time) bool {
	return s != nil && now.Before(s.deadline) && s.models[model][location]
}

// Current is a non-blocking read. Callers must still check support freshness
// at dispatch time if they hold a snapshot beyond this read.
func (i *Inventory) Current(now time.Time) *Snapshot {
	if i == nil {
		return nil
	}
	snapshot := i.current.Load()
	if snapshot == nil || !now.Before(snapshot.deadline) {
		return nil
	}
	return snapshot
}

// Start runs one immediate and then periodic credential-free refreshes per
// service. No request or model-discovery callback fetches inventory.
func (i *Inventory) Start(ctx context.Context, proxyURL func() string, report func(RefreshStatus)) {
	i.start(ctx, func(ctx context.Context) ([]byte, error) {
		rawProxy := ""
		if proxyURL != nil {
			rawProxy = proxyURL()
		}
		transport, _, errProxy := proxyutil.BuildHTTPTransport(rawProxy)
		if errProxy != nil {
			// The proxy URL may contain credentials.
			return nil, fmt.Errorf("invalid Vertex inventory HTTP proxy")
		}
		client := &http.Client{CheckRedirect: refuseRedirectFork}
		if transport != nil {
			client.Transport = transport
			defer transport.CloseIdleConnections()
		}
		return fetchOfficialFork(ctx, client)
	}, time.Now, nil, report)
}

func (i *Inventory) start(ctx context.Context, fetch fetchFork, now func() time.Time, events <-chan time.Time, report func(RefreshStatus)) bool {
	if i == nil || ctx == nil || ctx.Err() != nil || !i.running.CompareAndSwap(false, true) {
		return false
	}
	i.current.Store(nil)
	go func() {
		defer func() {
			i.current.Store(nil)
			i.running.Store(false)
		}()
		ticks := events
		if ticks == nil {
			ticker := time.NewTicker(refreshIntervalFork)
			defer ticker.Stop()
			ticks = ticker.C
		}
		i.refresh(ctx, fetch, now, report)
		for {
			select {
			case <-ctx.Done():
				return
			case _, open := <-ticks:
				if !open {
					return
				}
				i.refresh(ctx, fetch, now, report)
			}
		}
	}()
	return true
}

func (i *Inventory) refresh(ctx context.Context, fetch fetchFork, now func() time.Time, report func(RefreshStatus)) {
	if ctx.Err() != nil {
		return
	}
	body, errFetch := fetch(ctx)
	if errFetch == nil {
		var models modelRegionsFork
		models, errFetch = parseVerifiedMatrix(bytes.NewReader(body))
		if errFetch == nil && ctx.Err() == nil {
			timeVerified := now()
			deadline := timeVerified.Add(freshnessFork)
			i.current.Store(&Snapshot{models: models, verifiedAt: timeVerified, deadline: deadline})
			if report != nil {
				report(RefreshStatus{State: "fresh", VerifiedAt: timeVerified, FreshUntil: deadline, ModelCount: len(models)})
			}
			return
		}
	}
	if ctx.Err() != nil || report == nil {
		return
	}
	status := RefreshStatus{State: "unavailable", Err: errFetch}
	if last := i.current.Load(); last != nil {
		status.VerifiedAt = last.verifiedAt
		status.FreshUntil = last.deadline
		status.ModelCount = len(last.models)
		if now().Before(last.deadline) {
			status.State = "last_good"
		} else {
			status.State = "expired"
		}
	}
	report(status)
}

func refuseRedirectFork(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func fetchOfficialFork(ctx context.Context, client *http.Client) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeoutFork)
	defer cancel()
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, OfficialURL, nil)
	if errRequest != nil {
		return nil, fmt.Errorf("could not build Vertex inventory request")
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("Cache-Control", "no-cache")
	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("Vertex inventory HTTP request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	// No conditional request is made. An unsolicited 304 or partial response
	// is not provenance and must never extend the freshness deadline.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Vertex inventory returned HTTP %d", resp.StatusCode)
	}
	contentType, _, errType := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if errType != nil || contentType != "text/html" || resp.Header.Get("Content-Range") != "" || resp.ContentLength > maxDocumentFork {
		return nil, fmt.Errorf("Vertex inventory response was not a complete bounded HTML document")
	}
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, maxDocumentFork+1))
	if errRead != nil || len(body) > maxDocumentFork {
		return nil, fmt.Errorf("could not read complete bounded Vertex inventory response")
	}
	return body, nil
}

// Supports checks the newest published snapshot without waiting for refresh.
func (i *Inventory) Supports(model, location string, now time.Time) bool {
	return i.Current(now).Supports(model, location, now)
}
