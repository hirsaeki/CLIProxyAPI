package main

import (
	"os"
	"strings"
	"testing"
)

const matrixFixture = `<!doctype html><html><body>
<table><thead><tr><th>Model</th><th>Latest version</th></tr></thead><tbody>
<tr><td>Not a location table</td><td><code>gemini-ignore</code></td></tr>
</tbody></table>
<table><thead><tr><th></th><th>Global<br>(global)</th></tr></thead><tbody>
<tr><td class="vertex-ai-table-heading">Gemini models</td></tr>
<tr><td>Flash <code>(gemini-global)</code></td><td aria-label="Supported"></td></tr>
<tr><td>Unsupported <code>(gemini-unsupported)</code></td><td></td></tr>
<tr><td colspan="2" class="vertex-ai-table-heading">Embeddings models</td></tr>
<tr><td>Embedding <code>(gemini-embedding-001)</code></td><td aria-label="Supported"></td></tr>
</tbody></table>
<table><thead><tr><th></th><th>US (us)</th><th>EU (eu)</th></tr></thead><tbody>
<tr><td><a href="/models/gemini-multi">Gemini Multi</a> <code>(gemini-multi)</code></td><td aria-label="Supported"></td><td aria-label="Supported"></td></tr>
<tr><td>US only <code>(gemini-us-only)</code></td><td aria-label="Supported"></td><td></td></tr>
</tbody></table>
<table><thead><tr><th></th><th>Iowa (us-central1)</th><th>Belgium (europe-west1)</th></tr></thead><tbody>
<tr><td>Regional <code>(gemini-regional)</code></td><td aria-label="Supported"></td><td></td></tr>
<tr><td>Image <code>(imagen-4.0-generate-001)</code></td><td></td><td aria-label="Supported"></td></tr>
</tbody></table>
</body></html>`

func TestParseLocationMatrix(t *testing.T) {
	matrix, errParse := parseLocationMatrix(strings.NewReader(matrixFixture))
	if errParse != nil {
		t.Fatalf("parseLocationMatrix() error = %v", errParse)
	}

	assertSupported := func(location, model string, want bool) {
		t.Helper()
		models, okLocation := matrix[location]
		got := false
		for _, candidate := range models {
			if candidate.ID == model {
				got = true
				break
			}
		}
		if !okLocation || got != want {
			t.Fatalf("matrix[%q][%q] = %v, location=%v, want %v", location, model, got, okLocation, want)
		}
	}
	assertSupported("global", "gemini-global", true)
	assertSupported("global", "gemini-unsupported", false)
	assertSupported("global", "gemini-embedding-001", false)
	assertSupported("us", "gemini-multi", true)
	assertSupported("eu", "gemini-multi", true)
	assertSupported("us", "gemini-us-only", true)
	assertSupported("eu", "gemini-us-only", false)
	assertSupported("us-central1", "gemini-regional", true)
	assertSupported("europe-west1", "imagen-4.0-generate-001", true)
	if got := matrix["us"][0].DisplayName; got != "Gemini Multi" {
		t.Fatalf("matrix[us][0].DisplayName = %q, want Gemini Multi", got)
	}
	if _, exists := matrix["version"]; exists {
		t.Fatal("non-location table was parsed as a location matrix")
	}
}

func TestParseLocationMatrixOfficialSnapshot(t *testing.T) {
	path := os.Getenv("VERTEX_LOCATIONS_HTML")
	if path == "" {
		t.Skip("VERTEX_LOCATIONS_HTML is not set")
	}
	file, errOpen := os.Open(path)
	if errOpen != nil {
		t.Fatalf("open official locations snapshot: %v", errOpen)
	}
	defer func() {
		if errClose := file.Close(); errClose != nil {
			t.Errorf("close official locations snapshot: %v", errClose)
		}
	}()
	matrix, errParse := parseLocationMatrix(file)
	if errParse != nil {
		t.Fatalf("parseLocationMatrix() official snapshot error = %v", errParse)
	}
	for _, location := range []string{"global", "us", "eu", "us-central1"} {
		if len(matrix[location]) == 0 {
			t.Fatalf("official matrix location %q has no supported models", location)
		}
	}
	globalIDs := make(map[string]struct{}, len(matrix["global"]))
	for _, model := range matrix["global"] {
		globalIDs[model.ID] = struct{}{}
	}
	if _, okPreview := globalIDs["gemini-3.1-pro-preview"]; !okPreview {
		t.Fatal("official global matrix is missing gemini-3.1-pro-preview")
	}
	if _, hasUnversioned := globalIDs["gemini-3.1-pro"]; hasUnversioned {
		t.Fatal("official global matrix unexpectedly contains gemini-3.1-pro")
	}
}

func TestParseLocationMatrixRejectsMissingAvailability(t *testing.T) {
	_, errParse := parseLocationMatrix(strings.NewReader(`<table><tr><td>no matrix</td></tr></table>`))
	if errParse == nil {
		t.Fatal("parseLocationMatrix() error = nil, want missing matrix error")
	}
}
