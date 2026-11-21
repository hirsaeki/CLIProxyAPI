package vertexlocations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const markerFork = `<td><span aria-label="Supported"></span></td>`

func fixtureFork(pairs map[string][]string) string {
	var page strings.Builder
	page.WriteString("<!doctype html><html><body><h2>" + googleSectionFork + "</h2>")
	for _, group := range regionGroupsFork {
		page.WriteString("<table><thead><tr><th></th>")
		for _, location := range group {
			fmt.Fprintf(&page, "<th>Label<br>(%s)</th>", location)
		}
		page.WriteString(`</tr></thead><tbody><tr><td class="vertex-ai-table-heading" colspan="99">Gemini models</td></tr>`)
		// Keep completeness independent of which demo model is added or removed.
		row := func(id string, regions []string) {
			fmt.Fprintf(&page, "<tr><td>Display <code>(%s)</code></td>", id)
			for _, location := range group {
				supported := false
				for _, region := range regions {
					supported = supported || location == region
				}
				if supported {
					page.WriteString(markerFork)
				} else {
					page.WriteString("<td></td>")
				}
			}
			page.WriteString("</tr>")
		}
		row("gemini-anchor", group)
		row("['gemini-3.5-transcribe', 'gemini-3.5-transcribe-live']", group)
		row("['gemini-3.5-live-translate-preview']", group)
		for model, regions := range pairs {
			row(model, regions)
		}
		page.WriteString("</tbody></table>")
	}
	page.WriteString("<h2>" + partnerSectionFork + "</h2></body></html>")
	return page.String()
}

func TestFirstPartyMatrixVerificationFork(t *testing.T) {
	snapshot := fixtureFork(map[string][]string{"gemini-example-preview": {"eu", "europe-west4"}})
	models, err := parseVerifiedMatrix(strings.NewReader(snapshot))
	if err != nil || !models["gemini-example-preview"]["eu"] || !models["gemini-example-preview"]["europe-west4"] ||
		models["gemini-example-preview"]["us"] || models["gemini-example"]["eu"] || models["gemini-3.5-transcribe"] != nil {
		t.Fatalf("wrong exact first-party evidence: models=%v err=%v", models, err)
	}
	section := "<h2>" + googleSectionFork + "</h2>"
	end := "<h2>" + partnerSectionFork + "</h2>"
	for _, test := range []struct{ name, data string }{
		{"truncated-section", strings.Replace(snapshot, end, "", 1)},
		{"partial-region-group", strings.Replace(snapshot, "(us-east5)", "(unknown)", 1)},
		{"misaligned-rows", strings.Replace(snapshot, markerFork, "", 1)},
		{"spanned-rows", strings.Replace(snapshot, markerFork, `<td colspan="2" aria-label="Supported"></td>`, 1)},
		{"conflicting-markers", strings.Replace(snapshot, markerFork, `<td aria-label="Not supported"><span aria-label="Supported"></span></td>`, 1)},
		{"missing-category", strings.Replace(snapshot, "Gemini models", "Other Models", 1)},
		{"ambiguous-model-array", strings.Replace(snapshot, "'gemini-3.5-transcribe'", "'gemini-text'", 1)},
		{"unterminated-model-id", strings.Replace(snapshot, "(gemini-anchor)", "(gemini-anchor", 1)},
		{"conflicting-unknown-markers", strings.Replace(snapshot, markerFork, `<td aria-label="Unknown"><span aria-label="Supported"></span></td>`, 1)},
		{"ambiguous-model-cell", strings.Replace(snapshot, "<code>(gemini-anchor)</code>", "<code>(gemini-anchor)</code><code>(gemini-another)</code>", 1)},
		{"partner-table-only", strings.Replace(snapshot, section, end, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseVerifiedMatrix(strings.NewReader(test.data)); err == nil {
				t.Fatal("accepted unverifiable inventory")
			}
		})
	}
}

func TestFirstPartyMatrixAllowsOnlyObservedEmptyGroupFork(t *testing.T) {
	page := fixtureFork(map[string][]string{"gemini-example": {"global", "us", "eu"}})
	start := strings.LastIndex(page, "<table>")
	empty := strings.ReplaceAll(page[start:], markerFork, "<td></td>")
	emptyPage := page[:start] + empty
	models, err := parseVerifiedMatrix(strings.NewReader(emptyPage))
	if err != nil || !models["gemini-example"]["global"] || !models["gemini-example"]["us"] ||
		!models["gemini-example"]["eu"] || models["gemini-anchor"]["me-west1"] ||
		models["gemini-anchor"]["me-central1"] || models["gemini-anchor"]["me-central2"] {
		t.Fatalf("blank Middle East cells must contribute no positive evidence: %v", err)
	}
	now := time.Unix(1_000_000, 0)
	var inventory Inventory
	refresh := func(data string) {
		inventory.refresh(context.Background(), func(context.Context) ([]byte, error) { return []byte(data), nil },
			func() time.Time { return now }, nil)
	}
	refresh(page)
	if !inventory.Supports("gemini-anchor", "me-west1", now) {
		t.Fatal("fixture must initially publish Middle East support")
	}
	refresh(emptyPage)
	if inventory.Supports("gemini-anchor", "me-west1", now) || !inventory.Supports("gemini-example", "eu", now) {
		t.Fatal("complete blank grid must revoke prior support without losing other groups")
	}
	for name, malformed := range map[string]string{
		"nonempty-unknown-middle-east": page[:start] + strings.Replace(empty, "<td></td>", "<td><span class=changed></span></td>", 1),
		"marked-middle-east":           page[:start] + strings.Replace(empty, "<td></td>", "<td aria-label=Unknown></td>", 1),
		"missing-cell-middle-east":     page[:start] + strings.Replace(empty, "<td></td>", "", 1),
		"all-groups-empty":             strings.ReplaceAll(page, markerFork, "<td></td>"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseVerifiedMatrix(strings.NewReader(malformed)); err == nil {
				t.Fatal("accepted structurally unverified evidence")
			}
		})
	}
}

func TestInventoryHardFreshnessAndReplacementFork(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	var i Inventory
	if i.Current(now) != nil || i.Supports("gemini-old", "us", now) {
		t.Fatal("cold inventory produced evidence")
	}
	ctx := context.Background()
	refresh := func(text string, failure error) {
		i.refresh(ctx, func(context.Context) ([]byte, error) { return []byte(text), failure }, clock, nil)
	}
	refresh(fixtureFork(map[string][]string{"gemini-old": {"us"}}), nil)
	first := i.Current(now)
	if !first.Supports("gemini-old", "us", now) {
		t.Fatal("valid refresh did not publish")
	}
	deadline := first.deadline
	now = deadline.Add(-time.Second)
	refresh("", errors.New("network failure"))
	refresh("<html>partial", nil)
	if i.Current(now) != first || !i.Supports("gemini-old", "us", now) || i.Current(now).deadline != deadline {
		t.Fatal("failed/partial refresh changed last valid deadline")
	}
	now = deadline
	for range 3 {
		refresh("", errors.New("repeat failure"))
		now = now.Add(time.Hour)
		if i.Current(now) != nil || first.Supports("gemini-old", "us", now) {
			t.Fatal("stale fallback extended past hard deadline")
		}
	}
	refresh(fixtureFork(map[string][]string{"gemini-new": {"eu"}}), nil)
	next := i.Current(now)
	if next == nil || next == first || next.Supports("gemini-old", "us", now) || !next.Supports("gemini-new", "eu", now) {
		t.Fatal("new validated snapshot did not replace removed/added models")
	}
	if !first.Supports("gemini-old", "us", deadline.Add(-time.Second)) {
		t.Fatal("older immutable snapshot was mutated")
	}
}

func TestInventoryStartupPeriodicCoalescesFork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var i Inventory
	var calls atomic.Int32
	entered := make(chan int32, 2)
	finish := make(chan []byte)
	ticks := make(chan time.Time, 1)
	fetch := func(ctx context.Context) ([]byte, error) {
		entered <- calls.Add(1)
		select {
		case data := <-finish:
			return data, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if !i.start(ctx, fetch, time.Now, ticks, nil) {
		t.Fatal("failed to start")
	}
	waitStarted := func(want int32) {
		t.Helper()
		select {
		case n := <-entered:
			if n != want {
				t.Fatalf("fetch %d, want %d", n, want)
			}
		case <-ctx.Done():
			t.Fatal("background refresh never entered")
		}
	}
	waitStarted(1)
	if i.start(ctx, fetch, time.Now, ticks, nil) || i.Current(time.Now()) != nil || calls.Load() != 1 {
		t.Fatal("coalescing/cold read blocked or returned evidence")
	}
	finish <- []byte(fixtureFork(map[string][]string{"gemini-start": {"us"}}))
	ticks <- time.Now()
	waitStarted(2)
	if calls.Load() != 2 || !i.Supports("gemini-start", "us", time.Now()) {
		t.Fatal("periodic refresh failed to share startup evidence")
	}
}

type fakeTransportFork func(*http.Request) (*http.Response, error)

func (f fakeTransportFork) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchOfficialCompletePublicOnlyFork(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotModified, http.StatusPartialContent, http.StatusFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls int
			client := &http.Client{Transport: fakeTransportFork(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != OfficialURL || r.Method != http.MethodGet || r.Header.Get("Authorization") != "" ||
					r.Header.Get("Cookie") != "" || r.Header.Get("If-None-Match") != "" || r.Header.Get("Cache-Control") != "no-cache" {
					t.Fatalf("unexpected public inventory fetch %v", r.URL)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "Location": []string{"https://example.invalid/"}}, Body: io.NopCloser(strings.NewReader("document"))}, nil
			}), CheckRedirect: refuseRedirectFork}
			got, err := fetchOfficialFork(context.Background(), client)
			if calls != 1 || (status == http.StatusOK && (string(got) != "document" || err != nil)) || (status != http.StatusOK && err == nil) {
				t.Fatalf("status=%d calls=%d result=%q err=%v", status, calls, got, err)
			}
		})
	}
}
