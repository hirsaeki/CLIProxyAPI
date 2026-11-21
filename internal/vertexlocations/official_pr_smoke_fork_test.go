package vertexlocations

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	inventory.refresh(ctx, func(ctx context.Context) ([]byte, error) {
		return fetchOfficialFork(ctx, client)
	}, time.Now, func(value RefreshStatus) { status = value })

	now := time.Now()
	snapshot := inventory.Current(now)
	if status.State != "fresh" || snapshot == nil || status.Err != nil ||
		status.FreshUntil.Sub(status.VerifiedAt) != freshnessFork {
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
