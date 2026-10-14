package helps

import (
	"testing"

	"github.com/tidwall/sjson"
)

func TestAntigravityFalseSchemaValidationFork(t *testing.T) {
	for _, declarationsKey := range []string{"functionDeclarations", "function_declarations"} {
		for _, schemaKey := range []string{"parameters", "parametersJsonSchema", "parameters_json_schema", "response", "responseJsonSchema", "response_json_schema"} {
			path := "request.tools.0." + declarationsKey + ".0." + schemaKey
			for _, schema := range []string{`false`, `{"anyOf":[false]}`, `{"properties":{"x":{"oneOf":[false]}}}`} {
				payload, err := sjson.SetRaw("{}", path, schema)
				if err != nil {
					t.Fatal(err)
				}
				if ValidateAntigravityFalseSchemas([]byte(payload)) == nil {
					t.Fatalf("missed unsupported schema at %s: %s", path, payload)
				}
			}
		}
	}
	for _, configKey := range []string{"generationConfig", "generation_config"} {
		for _, schemaKey := range []string{"responseSchema", "responseJsonSchema", "response_schema", "response_json_schema"} {
			path := "request." + configKey + "." + schemaKey
			payload, err := sjson.SetRaw("{}", path, `{"oneOf":[false]}`)
			if err != nil {
				t.Fatal(err)
			}
			if ValidateAntigravityFalseSchemas([]byte(payload)) == nil {
				t.Fatalf("missed unsupported response schema at %s", path)
			}
		}
	}
	payload := []byte(`{"request":{"contents":[{"parts":[{"functionCall":{"args":{"anyOf":[false]}}}]}],"tools":[{"functionDeclarations":[{"parameters":{"type":"object","properties":{"flag":{"type":"boolean","enum":[false],"default":false}},"examples":[{"anyOf":[false]}]}}]}],"generationConfig":{"responseSchema":{"type":"object","additionalProperties":false}}}}`)
	if err := ValidateAntigravityFalseSchemas(payload); err != nil {
		t.Fatalf("rejected data or supported additionalProperties constraint: %v", err)
	}
}
