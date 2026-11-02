package util

import (
	"testing"
)

func TestAllFalseSchemaUnionPreservedFork(t *testing.T) {
	cleaners := map[string]func(string) string{
		"gemini":      CleanJSONSchemaForGemini,
		"json-schema": CleanJSONSchemaForGeminiJSONSchema,
		"antigravity": CleanJSONSchemaForAntigravity,
		"tool":        func(s string) string { return CleanJSONSchemaForAntigravityTool(s, false) },
		"claude":      func(s string) string { return CleanJSONSchemaForAntigravityClaudeTool(s, false) },
		"response":    CleanJSONSchemaForAntigravityResponse,
	}
	cases := []struct {
		name  string
		input string
		path  string
	}{
		{"any-of", `{"anyOf":[false]}`, "anyOf"},
		{"one-of", `{"oneOf":[false,false]}`, "oneOf"},
		{"properties", `{"type":"object","properties":{"x":{"type":"string"}},"anyOf":[false]}`, "anyOf"},
		{"sibling-any-of", `{"anyOf":[false],"oneOf":[{"type":"string"},{"type":"integer"}]}`, "anyOf"},
		{"sibling-one-of", `{"oneOf":[false],"anyOf":[{"type":"string"},{"type":"integer"}]}`, "oneOf"},
		{"nested", `{"type":"object","properties":{"value":{"anyOf":[false]}}}`, "properties.value.anyOf"},
		{"escaped-name", `{"type":"object","properties":{"a.b":{"oneOf":[false]}}}`, `properties.a\.b.oneOf`},
		{"array", `{"type":"array","items":{"anyOf":[false]}}`, "items.anyOf"},
		{"wrapper", `{"schema":{"anyOf":[false]}}`, "schema.anyOf"},
	}
	for cleanerName, clean := range cleaners {
		for _, tc := range cases {
			t.Run(cleanerName+"/"+tc.name, func(t *testing.T) {
				cleaned := clean(tc.input)
				if !allFalseSchemaUnion(GetGJSONBytesNoCopy([]byte(cleaned), tc.path).Array()) {
					t.Fatalf("unsatisfiable union was lost: %s", cleaned)
				}
				compareJSON(t, cleaned, clean(cleaned))
				chained := CleanJSONSchemaForAntigravityClaudeTool(CleanJSONSchemaForAntigravity(tc.input), false)
				if !allFalseSchemaUnion(GetGJSONBytesNoCopy([]byte(chained), tc.path).Array()) {
					t.Fatalf("translator/executor cleaning lost unsatisfiable union: %s", chained)
				}
			})
		}
	}
}

func TestFalseSchemaUnionDataAndMixedBranchesFork(t *testing.T) {
	for _, key := range []string{"default", "examples", "enum"} {
		input := `{"type":"object","` + key + `":[{"anyOf":[false],"oneOf":[false]}]}`
		compareJSON(t, input, flattenAnyOfOneOf(input, true, false))
	}
	for _, input := range []string{
		`{"anyOf":[false,{"type":"string"}]}`,
		`{"oneOf":[false,{"type":"null"}]}`,
		`{"anyOf":[false,true]}`,
	} {
		cleaned := CleanJSONSchemaForGeminiJSONSchema(input)
		if ContainsFalseSchema(cleaned) {
			t.Fatalf("satisfiable mixed union retained an unnecessary false branch: %s", cleaned)
		}
	}
	for _, input := range []string{`[]`, `[null]`, `[{"required":["x"]}]`, `[false,true]`} {
		if allFalseSchemaUnion(ParseGJSONBytesNoCopy([]byte(input)).Array()) {
			t.Fatalf("non-all-false input classified as unsatisfiable: %s", input)
		}
	}
}

func TestContainsFalseSchemaScopesSchemaNodesFork(t *testing.T) {
	for _, input := range []string{
		`false`, `{"anyOf":[false]}`, `{"properties":{"x":false}}`,
		`{"items":{"oneOf":[false]}}`, `{"allOf":[false]}`,
		`{"contains":{"anyOf":[false]}}`, `{"then":{"oneOf":[false]}}`,
	} {
		if !ContainsFalseSchema(input) {
			t.Fatalf("missed unsupported false schema: %s", input)
		}
	}
	for _, input := range []string{
		``, `true`, `{"additionalProperties":false}`,
		`{"type":"boolean","enum":[false],"default":false,"examples":[{"anyOf":[false]}]}`,
		`{"properties":{"anyOf":{"type":"boolean"},"oneOf":{"type":"string"}}}`,
	} {
		if ContainsFalseSchema(input) {
			t.Fatalf("ordinary data or supported constraint treated as a false schema: %s", input)
		}
	}
}

func TestToolSearchAllFalseSchemaPreservedFork(t *testing.T) {
	tool := ParseGJSONBytesNoCopy([]byte(`{"type":"tool_search","execution":"client","parameters":{"type":"object","anyOf":[false]}}`))
	got := buildGeminiToolSearchParametersSchema(tool)
	if !allFalseSchemaUnion(GetGJSONBytesNoCopy(got, "anyOf").Array()) {
		t.Fatalf("tool search substituted a permissive default: %s", got)
	}
}

func TestContainsFalseSchemaDuplicateMapKeysFork(t *testing.T) {
	for _, keyword := range falseSchemaMapKeywords {
		for _, entries := range []string{
			`"x":true,"x":false`,
			`"x":false,"x":true`,
			`"x":{"type":"string"},"x":{"anyOf":[false]}`,
			`"x":{"anyOf":[false]},"x":{"type":"string"}`,
		} {
			input := `{"` + keyword + `":{` + entries + `}}`
			if !ContainsFalseSchema(input) {
				t.Fatalf("duplicate schema key hid false schema: %s", input)
			}
		}
	}
}

func TestFindSchemaUnionPathsPreservesDuplicateMapKeysFork(t *testing.T) {
	input := `{"properties":{"a.b":{"anyOf":[false]},"a.b":{"anyOf":[false]}}}`
	paths := findSchemaUnionPaths(input)["anyOf"]
	if len(paths) != 2 || paths[0] != `properties.a\.b.anyOf` || paths[1] != paths[0] {
		t.Fatalf("duplicate escaped property paths were collapsed or changed: %v", paths)
	}
	input = `{"properties":{"z":{"anyOf":[false]},"a":{"anyOf":[false]},"z":{"oneOf":[false]}}}`
	got := findSchemaUnionPaths(input)
	if len(got["anyOf"]) != 2 || got["anyOf"][0] != "properties.z.anyOf" || got["anyOf"][1] != "properties.a.anyOf" || len(got["oneOf"]) != 1 || got["oneOf"][0] != "properties.z.oneOf" {
		t.Fatalf("schema entry traversal lost source order or a duplicate: %v", got)
	}
}
