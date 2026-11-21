package vertexlocations

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

const (
	// OfficialURL is the public locations matrix consumed by both the native
	// routing inventory and the optional region model-discovery plugin.
	OfficialURL         = "https://docs.cloud.google.com/gemini-enterprise-agent-platform/resources/locations"
	googleSectionFork   = "Google model endpoint locations"
	partnerSectionFork  = "Google Cloud partner model endpoint locations"
	middleEastGroupFork = 6 // Index in regionGroupsFork.
)

var regionGroupsFork = [][]string{
	{"global"},
	{"us", "eu"},
	{"us-west1", "us-west4", "us-central1", "us-east1", "us-east4", "us-east5", "us-south1"},
	{"northamerica-northeast1", "southamerica-east1"},
	{"europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-west6", "europe-west8", "europe-west9", "europe-north1", "europe-central2", "europe-southwest1"},
	{"asia-south1", "asia-southeast1", "asia-east1", "asia-east2", "asia-northeast1", "asia-northeast3", "australia-southeast1"},
	{"me-west1", "me-central1", "me-central2"},
}

var knownRegionsFork = func() map[string]struct{} {
	out := make(map[string]struct{})
	for _, group := range regionGroupsFork {
		for _, location := range group {
			out[location] = struct{}{}
		}
	}
	return out
}()

// KnownLocation is a canonical destination from the documented Vertex grid.
// The endpoint name by itself never establishes support for any model.
func KnownLocation(location string) bool {
	_, ok := knownRegionsFork[location]
	return ok
}

type modelRegionsFork map[string]map[string]bool

// parseVerifiedMatrix requires a complete first-party Google document section.
// Unlike discovery, it accepts explicit Gemini-group entries only and can
// never turn a partial or structurally changed response into routing evidence.
func parseVerifiedMatrix(reader io.Reader) (modelRegionsFork, error) {
	root, errParse := html.Parse(reader)
	if errParse != nil {
		return nil, fmt.Errorf("parse Vertex documentation: %w", errParse)
	}
	var began, ended bool
	var validationErr error
	verifiedGroups := make(map[int]bool, len(regionGroupsFork))
	models := make(modelRegionsFork)
	visitDescendants(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		if n.Data == "h2" {
			title := nodeText(n)
			if title == googleSectionFork {
				if began {
					validationErr = fmt.Errorf("duplicate first-party Google inventory section")
					return false
				}
				began = true
				return true
			}
			if began && !ended {
				if title != partnerSectionFork {
					validationErr = fmt.Errorf("missing end of first-party Google inventory section")
					return false
				}
				ended = true
			}
		}
		if !began || ended || n.Data != "table" {
			return true
		}
		locations, rows, errTable := verifiedTableFork(n)
		if errTable != nil {
			validationErr = errTable
			return false
		}
		if len(locations) == 0 {
			return true
		}
		group := locationGroupFork(locations)
		if group < 0 || verifiedGroups[group] {
			validationErr = fmt.Errorf("unknown or duplicate Gemini location group")
			return false
		}
		verifiedGroups[group] = true
		for model, support := range rows {
			if models[model] == nil {
				models[model] = make(map[string]bool)
			}
			for col, location := range locations {
				if support[col] {
					models[model][location] = true
				}
			}
		}
		return true
	})
	if validationErr != nil {
		return nil, validationErr
	}
	if !began || !ended || len(verifiedGroups) != len(regionGroupsFork) {
		return nil, fmt.Errorf("incomplete first-party Gemini location inventory (%d/%d verified location groups)", len(verifiedGroups), len(regionGroupsFork))
	}
	return models, nil
}

func locationGroupFork(locations []string) int {
	found := make(map[string]bool, len(locations))
	for _, location := range locations {
		if found[location] || !KnownLocation(location) {
			return -1
		}
		found[location] = true
	}
	for index, expected := range regionGroupsFork {
		if len(expected) != len(found) {
			continue
		}
		all := true
		for _, location := range expected {
			all = all && found[location]
		}
		if all {
			return index
		}
	}
	return -1
}

func verifiedTableFork(table *html.Node) ([]string, map[string][]bool, error) {
	bodies := directChildren(table, "tbody")
	var rows []*html.Node
	for _, body := range bodies {
		rows = append(rows, directChildren(body, "tr")...)
	}
	gemini := func(cell *html.Node) bool {
		return strings.EqualFold(nodeText(cell), "Gemini models") && hasClass(cell, "vertex-ai-table-heading")
	}
	present := false
	for _, row := range rows {
		cells := directChildren(row, "td")
		if len(cells) == 1 && gemini(cells[0]) {
			present = true
		}
	}
	if !present {
		return nil, nil, nil
	}

	heads := directChildren(table, "thead")
	if len(heads) != 1 || len(bodies) != 1 {
		return nil, nil, fmt.Errorf("Gemini inventory table must have one head and body")
	}
	headerRows := directChildren(heads[0], "tr")
	if len(headerRows) != 1 {
		return nil, nil, fmt.Errorf("Gemini inventory table has unexpected header rows")
	}
	headers := directChildren(headerRows[0], "th")
	if len(headers) < 2 || len(headers) != elementCountFork(headerRows[0]) {
		return nil, nil, fmt.Errorf("Gemini inventory table has invalid headers")
	}
	var locations []string
	for _, cell := range headers {
		if hasSpanFork(cell) {
			return nil, nil, fmt.Errorf("Gemini inventory has spanning column headers")
		}
	}
	for _, cell := range headers[1:] {
		match := locationSuffixPattern.FindStringSubmatch(strings.ToLower(nodeText(cell)))
		if len(match) != 2 {
			return nil, nil, fmt.Errorf("Gemini inventory table has ambiguous location header")
		}
		locations = append(locations, match[1])
	}
	models := make(map[string][]bool)
	var inGemini, sawGemini bool
	allTextSupportCellsBlank := true
	for _, row := range rows {
		cells := directChildren(row, "td")
		if len(cells) == 1 && hasClass(cells[0], "vertex-ai-table-heading") {
			inGemini = gemini(cells[0])
			if inGemini {
				if sawGemini {
					return nil, nil, fmt.Errorf("Gemini inventory table has duplicate Gemini group")
				}
				sawGemini = true
			}
			continue
		}
		if !inGemini {
			continue
		}
		if len(cells) != len(headers) || len(cells) != elementCountFork(row) {
			return nil, nil, fmt.Errorf("Gemini inventory row has mismatched columns")
		}
		for _, cell := range cells {
			if hasSpanFork(cell) {
				return nil, nil, fmt.Errorf("Gemini inventory row has spanning columns")
			}
		}
		model, omit := exactModelIDFork(cells[0])
		if omit {
			continue
		}
		if model == "" {
			return nil, nil, fmt.Errorf("Gemini inventory row has no exact model identifier")
		}
		if !TextModel(model) {
			continue
		}
		support := make([]bool, len(locations))
		previous := models[model]
		for col, cell := range cells[1:] {
			// Never infer support from a blank cell, a link or a similar ID.
			allTextSupportCellsBlank = allTextSupportCellsBlank && blankSupportCellFork(cell)
			supported, conflict := supportCellFork(cell)
			if conflict {
				return nil, nil, fmt.Errorf("Gemini inventory cell has conflicting support markers")
			}
			support[col] = supported && (previous == nil || previous[col])
		}
		models[model] = support
	}
	if !sawGemini || len(models) == 0 {
		return nil, nil, fmt.Errorf("Gemini inventory table has no exact model rows")
	}
	positive := false
	for _, support := range models {
		for _, supported := range support {
			positive = positive || supported
		}
	}
	// The official Middle East grid can contain only empty text-model
	// cells. It contributes no evidence, while all other groups must still
	// include an explicit positive. Nonempty unknown markup fails closed.
	if !positive && !(allTextSupportCellsBlank && locationGroupFork(locations) == middleEastGroupFork) {
		return nil, nil, fmt.Errorf("Gemini inventory table has no positive support markers")
	}
	return locations, models, nil
}

// TextModel excludes known non-text Gemini families. The executor still
// checks the request's actual protocol, action and translated payload.
func TextModel(model string) bool {
	if !strings.HasPrefix(model, "gemini-") || strings.Contains(model, "/") {
		return false
	}
	for _, class := range []string{"-image", "-tts", "-embedding", "-live", "-omni", "-audio", "-robot", "-transcribe", "-nano-banana"} {
		if strings.Contains(model, class) {
			return false
		}
	}
	return true
}

func exactModelIDFork(cell *html.Node) (id string, omit bool) {
	var raw string
	count := 0
	visitDescendants(cell, func(node *html.Node) bool {
		if node.Type == html.ElementNode && node.Data == "code" {
			count++
			raw = strings.TrimSpace(nodeText(node))
		}
		return true
	})
	if count != 1 {
		return "", false
	}
	opened, closed := strings.HasPrefix(raw, "("), strings.HasSuffix(raw, ")")
	if opened != closed || (opened && len(raw) < 3) {
		return "", false
	}
	if opened {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	if modelIDPattern.MatchString(raw) {
		return raw, false
	}
	// The current official grid renders transcription and live workloads as
	// array-shaped code cells within the Gemini category. Recognize only
	// unambiguous non-text arrays, and never publish evidence from them.
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		for _, item := range strings.Split(raw[1:len(raw)-1], ",") {
			item = strings.TrimSpace(item)
			if len(item) < 3 || item[0] != '\'' || item[len(item)-1] != '\'' {
				return "", false
			}
			item = item[1 : len(item)-1]
			if !modelIDPattern.MatchString(item) || !strings.HasPrefix(item, "gemini-") || TextModel(item) {
				return "", false
			}
		}
		return "", true
	}
	return "", false
}

func supportCellFork(cell *html.Node) (supported, conflict bool) {
	positive, other := 0, 0
	visitDescendants(cell, func(node *html.Node) bool {
		for _, attr := range node.Attr {
			if attr.Key != "aria-label" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(attr.Val), "Supported") {
				positive++
			} else {
				other++
			}
		}
		return true
	})
	return positive == 1 && other == 0, positive > 1 || positive == 1 && other > 0
}

func blankSupportCellFork(cell *html.Node) bool {
	return len(cell.Attr) == 0 && elementCountFork(cell) == 0 && strings.TrimSpace(nodeText(cell)) == ""
}

func hasSpanFork(cell *html.Node) bool {
	for _, attr := range cell.Attr {
		if (attr.Key == "rowspan" || attr.Key == "colspan") && attr.Val != "1" {
			return true
		}
	}
	return false
}

func elementCountFork(node *html.Node) int {
	count := 0
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			count++
		}
	}
	return count
}
