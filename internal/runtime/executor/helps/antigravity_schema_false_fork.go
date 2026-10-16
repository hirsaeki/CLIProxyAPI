package helps

import (
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	"github.com/tidwall/gjson"
)

// ValidateAntigravityFalseSchemas runs after payload rules, without rewriting
// their output. Antigravity converts tool/response schemas into native protobuf
// Schema messages, which cannot carry a boolean false schema. Fail locally rather
// than silently broadening the constraint or sending an unsupported schema.
func ValidateAntigravityFalseSchemas(payload []byte) error {
	request := gjson.GetBytes(payload, "request")
	for i, tool := range request.Get("tools").Array() {
		for _, declarationsKey := range []string{"functionDeclarations", "function_declarations"} {
			for j, declaration := range tool.Get(declarationsKey).Array() {
				for _, schemaKey := range []string{"parameters", "parametersJsonSchema", "parameters_json_schema", "response", "responseJsonSchema", "response_json_schema"} {
					if util.ContainsFalseSchema(declaration.Get(schemaKey).Raw) {
						return fmt.Errorf("antigravity: native schema cannot represent a false schema in request.tools.%d.%s.%d.%s", i, declarationsKey, j, schemaKey)
					}
				}
			}
		}
	}
	for _, configKey := range []string{"generationConfig", "generation_config"} {
		for _, schemaKey := range []string{"responseSchema", "responseJsonSchema", "response_schema", "response_json_schema"} {
			if util.ContainsFalseSchema(request.Get(configKey + "." + schemaKey).Raw) {
				return fmt.Errorf("antigravity: native schema cannot represent a false schema in request.%s.%s", configKey, schemaKey)
			}
		}
	}
	return nil
}
