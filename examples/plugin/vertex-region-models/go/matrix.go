package main

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

type documentedModel struct {
	ID          string
	DisplayName string
}

type locationMatrix map[string][]documentedModel

var (
	locationSuffixPattern = regexp.MustCompile(`\(([a-z0-9]+(?:-[a-z0-9]+)*)\)\s*$`)
	modelIDPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
)

func parseLocationMatrix(reader io.Reader) (locationMatrix, error) {
	doc, errParse := html.Parse(reader)
	if errParse != nil {
		return nil, fmt.Errorf("parse locations HTML: %w", errParse)
	}
	matrix := make(locationMatrix)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "table" {
			parseLocationTable(node, matrix)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	supported := 0
	for _, models := range matrix {
		supported += len(models)
	}
	if len(matrix) == 0 || supported == 0 {
		return nil, fmt.Errorf("locations HTML did not contain a supported model matrix")
	}
	return matrix, nil
}

func parseLocationTable(table *html.Node, matrix locationMatrix) {
	thead := firstDescendant(table, "thead")
	headerRow := firstDescendant(thead, "tr")
	headers := directChildren(headerRow, "th")
	if len(headers) < 2 {
		return
	}
	locations := make([]string, len(headers)-1)
	for index := 1; index < len(headers); index++ {
		match := locationSuffixPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(nodeText(headers[index]))))
		if len(match) != 2 {
			return
		}
		locations[index-1] = match[1]
	}
	for _, location := range locations {
		if matrix[location] == nil {
			matrix[location] = []documentedModel{}
		}
	}

	tbody := firstDescendant(table, "tbody")
	category := ""
	for _, row := range directChildren(tbody, "tr") {
		cells := directChildren(row, "td")
		if len(cells) == 1 && hasClass(cells[0], "vertex-ai-table-heading") {
			category = strings.ToLower(strings.TrimSpace(nodeText(cells[0])))
			continue
		}
		if category != "" && category != "gemini models" {
			continue
		}
		if len(cells) < 2 {
			continue
		}
		model := documentedModelFromCell(cells[0])
		if model.ID == "" {
			continue
		}
		for index, location := range locations {
			cellIndex := index + 1
			if cellIndex < len(cells) && hasSupportedLabel(cells[cellIndex]) {
				matrix[location] = appendDocumentedModel(matrix[location], model)
			}
		}
	}
}

func hasClass(node *html.Node, target string) bool {
	if node == nil {
		return false
	}
	for _, attr := range node.Attr {
		if !strings.EqualFold(attr.Key, "class") {
			continue
		}
		for _, className := range strings.Fields(attr.Val) {
			if className == target {
				return true
			}
		}
	}
	return false
}

func appendDocumentedModel(models []documentedModel, model documentedModel) []documentedModel {
	for _, existing := range models {
		if existing.ID == model.ID {
			return models
		}
	}
	return append(models, model)
}

func documentedModelFromCell(cell *html.Node) documentedModel {
	modelID := strings.ToLower(modelIDFromCell(cell))
	if modelID == "" {
		return documentedModel{}
	}
	displayName := ""
	if link := firstDescendant(cell, "a"); link != nil {
		displayName = strings.TrimSpace(nodeText(link))
	}
	if displayName == "" {
		displayName = strings.TrimSpace(strings.Replace(nodeText(cell), "("+modelID+")", "", 1))
	}
	if strings.HasSuffix(modelID, "-preview") && !strings.Contains(strings.ToLower(displayName), "preview") {
		displayName += " Preview"
	}
	return documentedModel{ID: modelID, DisplayName: strings.TrimSpace(displayName)}
}

func modelIDFromCell(cell *html.Node) string {
	var modelID string
	visitDescendants(cell, func(node *html.Node) bool {
		if node.Type != html.ElementNode || node.Data != "code" {
			return true
		}
		candidate := strings.TrimSpace(nodeText(node))
		candidate = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(candidate, "("), ")"))
		if modelIDPattern.MatchString(candidate) {
			modelID = candidate
			return false
		}
		return true
	})
	return modelID
}

func hasSupportedLabel(node *html.Node) bool {
	found := false
	visitDescendants(node, func(current *html.Node) bool {
		if current.Type == html.ElementNode {
			for _, attr := range current.Attr {
				if strings.EqualFold(attr.Key, "aria-label") && strings.EqualFold(strings.TrimSpace(attr.Val), "Supported") {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

func firstDescendant(node *html.Node, tag string) *html.Node {
	if node == nil {
		return nil
	}
	var found *html.Node
	visitDescendants(node, func(current *html.Node) bool {
		if current != node && current.Type == html.ElementNode && current.Data == tag {
			found = current
			return false
		}
		return true
	})
	return found
}

func directChildren(node *html.Node, tag string) []*html.Node {
	if node == nil {
		return nil
	}
	out := make([]*html.Node, 0)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == tag {
			out = append(out, child)
		}
	}
	return out
}

func visitDescendants(node *html.Node, visit func(*html.Node) bool) bool {
	if node == nil || !visit(node) {
		return false
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if !visitDescendants(child, visit) {
			return false
		}
	}
	return true
}

func nodeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	visitDescendants(node, func(current *html.Node) bool {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
		}
		return true
	})
	return strings.Join(strings.Fields(builder.String()), " ")
}
