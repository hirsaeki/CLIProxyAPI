package vertexlocations

import (
	"bytes"
	"context"
	"fmt"
	"golang.org/x/net/html"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This temporary smoke test runs only on the first attempt of the existing
// Linux PR build for #171. Ordinary unit tests and main remain offline.
func TestOfficialVertexInventoryPR171SmokeFork(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("GITHUB_ACTIONS") != "true" ||
		os.Getenv("GITHUB_REPOSITORY") != "hirsaeki/CLIProxyAPI" ||
		os.Getenv("GITHUB_EVENT_NAME") != "pull_request" || os.Getenv("GITHUB_WORKFLOW") != "pr-test-build" ||
		os.Getenv("GITHUB_JOB") != "build" || os.Getenv("GITHUB_RUN_ATTEMPT") != "1" ||
		os.Getenv("GITHUB_REF") != "refs/pull/171/merge" ||
		os.Getenv("GITHUB_HEAD_REF") != "feat/vertex-region-inventory-20261121" {
		t.Skip("live public inventory check is restricted to PR #171's Linux build")
	}
	// This job runs the package suite again after refreshing another catalog.
	// Allow the one fixed GET only once across both test process invocations.
	temp := os.Getenv("RUNNER_TEMP")
	if temp == "" {
		t.Fatal("Actions runner temp directory is required for one-shot guard")
	}
	marker, errMark := os.OpenFile(filepath.Join(temp, "cpa-pr171-vertex-html-smoke.once"),
		os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if os.IsExist(errMark) {
		t.Skip("official Vertex inventory smoke already attempted in this job")
	}
	if errMark != nil {
		t.Fatal("could not reserve the one-shot Actions smoke")
	}
	if errClose := marker.Close(); errClose != nil {
		t.Fatal("could not close the one-shot Actions smoke marker")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Do not load CPA config, proxy credentials, Google credentials or cookies.
	// The same bounded production public GET and strict parser are exercised.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: refuseRedirectFork}
	var inventory Inventory
	var status RefreshStatus
	var raw []byte
	inventory.refresh(ctx, func(ctx context.Context) ([]byte, error) {
		body, err := fetchOfficialFork(ctx, client)
		raw = body
		return body, err
	}, time.Now, func(value RefreshStatus) { status = value })

	now := time.Now()
	snapshot := inventory.Current(now)
	if status.State != "fresh" || snapshot == nil || status.Err != nil ||
		status.FreshUntil.Sub(status.VerifiedAt) != freshnessFork {
		if len(raw) > 0 {
			officialShapePR171Fork(t, raw)
		}
		t.Fatalf("official Vertex inventory did not publish fresh evidence (state=%q): %v", status.State, status.Err)
	}
	pairs := 0
	for _, regions := range snapshot.models {
		pairs += len(regions)
	}
	// The model-specific official availability reference lists all three of
	// these regions for Gemini 3.8 Flash Standard PayGo.
	const model = "gemini-3.8-flash"
	for _, location := range []string{"global", "us", "eu"} {
		if !snapshot.Supports(model, location, now) {
			t.Fatalf("fresh official Vertex inventory lacks expected exact support: %s / %s (models=%d, positive_pairs=%d)",
				model, location, status.ModelCount, pairs)
		}
	}
	line := fmt.Sprintf("Vertex official HTML smoke: fresh; models=%d; positive_pairs=%d; validated=3/3.", status.ModelCount, pairs)
	t.Log(line)
	// Successful go test output is normally quiet. Record just the result in
	// the existing Actions step summary, never the HTML or the full catalog.
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		file, errOpen := os.OpenFile(summary, os.O_WRONLY|os.O_APPEND, 0)
		if errOpen != nil {
			t.Fatal("could not open Actions step summary")
		}
		_, errWrite := fmt.Fprintln(file, line)
		errClose := file.Close()
		if errWrite != nil || errClose != nil {
			t.Fatal("could not record the Actions smoke result")
		}
	}
}

// Diagnostics intentionally emit only fixed category indices and enum counts.
func officialShapePR171Fork(t *testing.T, raw []byte) {
	root, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return
	}
	active := false
	visitDescendants(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		if n.Data == "h2" {
			active = nodeText(n) == googleSectionFork
		}
		if !active || n.Data != "table" {
			return true
		}
		var locations []string
		for _, head := range directChildren(n, "thead") {
			for _, row := range directChildren(head, "tr") {
				cells := directChildren(row, "th")
				for _, cell := range cells {
					if m := locationSuffixPattern.FindStringSubmatch(strings.ToLower(nodeText(cell))); len(m) == 2 {
						locations = append(locations, m[1])
					}
				}
			}
		}
		var saw, in bool
		var textRows, otherRows, positive, negative, unknown, blank, checkIcon, closeIcon, otherIcon, semanticText int
		for _, tb := range directChildren(n, "tbody") {
			for _, row := range directChildren(tb, "tr") {
				cells := directChildren(row, "td")
				if len(cells) == 1 && hasClass(cells[0], "vertex-ai-table-heading") {
					in = strings.EqualFold(nodeText(cells[0]), "Gemini models")
					saw = saw || in
					continue
				}
				if !in || len(cells) < 2 {
					continue
				}
				model, omit := exactModelIDFork(cells[0])
				if omit || !TextModel(model) {
					otherRows++
					continue
				}
				textRows++
				for _, cell := range cells[1:] {
					markers := 0
					visitDescendants(cell, func(node *html.Node) bool {
						for _, a := range node.Attr {
							value := strings.ToLower(strings.TrimSpace(a.Val))
							if a.Key == "aria-label" {
								markers++
								switch value {
								case "supported":
									positive++
								case "unsupported", "not supported":
									negative++
								default:
									unknown++
								}
							}
							if a.Key == "class" {
								for _, token := range strings.Fields(value) {
									switch token {
									case "check", "check_circle", "check-circle", "supported", "devsite-icon-check":
										checkIcon++
									case "close", "cancel", "not-supported", "devsite-icon-close":
										closeIcon++
									case "material-icons", "devsite-icon":
										otherIcon++
									}
								}
							}
						}
						return true
					})
					if markers == 0 && elementCountFork(cell) == 0 && strings.TrimSpace(nodeText(cell)) == "" {
						blank++
					}
					if text := strings.ToLower(strings.TrimSpace(nodeText(cell))); text == "supported" || text == "not supported" || text == "unsupported" || text == "check" || text == "close" {
						semanticText++
					}
				}
			}
		}
		if saw {
			_, _, strict := verifiedTableFork(n)
			noPositive := strict != nil && strict.Error() == "Gemini inventory table has no positive support markers"
			t.Logf("official shape group=%d columns=%d text_rows=%d other_rows=%d supported=%d unsupported=%d unknown=%d blank=%d check_class=%d close_class=%d generic_icon=%d known_text=%d strict_ok=%t no_positive=%t", locationGroupFork(locations), len(locations), textRows, otherRows, positive, negative, unknown, blank, checkIcon, closeIcon, otherIcon, semanticText, strict == nil, noPositive)
		}
		return true
	})
}
