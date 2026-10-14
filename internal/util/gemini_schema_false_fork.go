package util

import (
	"strconv"

	"github.com/tidwall/gjson"
)

var falseSchemaMapKeywords = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}

var falseSchemaSingleKeywords = []string{
	"items", "additionalProperties", "not", "if", "then", "else", "contains", "propertyNames",
	"unevaluatedProperties", "unevaluatedItems", "contentSchema", "additionalItems",
}

var falseSchemaArrayKeywords = []string{"anyOf", "oneOf", "allOf", "prefixItems"}

// allFalseSchemaUnion distinguishes an unsatisfiable union from malformed or
// required-only branches that the compatibility cleaner may also filter out.
func allFalseSchemaUnion(items []gjson.Result) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if item.Type != gjson.False {
			return false
		}
	}
	return true
}

// ContainsFalseSchema reports false schemas that a native protobuf Schema carrier
// cannot represent. It walks schema positions only, never enum/default/example data.
// additionalProperties:false is an object constraint, not an unrepresentable child
// schema, and remains allowed for native response schemas that support it.
func ContainsFalseSchema(schema string) bool {
	return containsFalseSchema(gjson.Parse(schema))
}

func containsFalseSchema(schema gjson.Result) bool {
	if schema.Type == gjson.False {
		return true
	}
	if !schema.IsObject() {
		return false
	}
	for _, key := range falseSchemaMapKeywords {
		found := false
		schema.Get(key).ForEach(func(_, child gjson.Result) bool {
			found = containsFalseSchema(child)
			return !found
		})
		if found {
			return true
		}
	}
	for _, key := range falseSchemaSingleKeywords {
		child := schema.Get(key)
		if key == "additionalProperties" && child.Type == gjson.False {
			continue
		}
		if child.IsArray() {
			for _, item := range child.Array() {
				if containsFalseSchema(item) {
					return true
				}
			}
		} else if containsFalseSchema(child) {
			return true
		}
	}
	for _, key := range falseSchemaArrayKeywords {
		for _, child := range schema.Get(key).Array() {
			if containsFalseSchema(child) {
				return true
			}
		}
	}
	return false
}

// findSchemaUnionPaths follows schema-bearing keywords rather than arbitrary JSON
// keys, so enum/default/example values containing anyOf or oneOf remain data.
func findSchemaUnionPaths(jsonStr string) map[string][]string {
	paths := map[string][]string{}
	var walk func(gjson.Result, string)
	walk = func(schema gjson.Result, path string) {
		if !schema.IsObject() {
			return
		}
		for _, key := range falseSchemaArrayKeywords {
			children := schema.Get(key)
			if !children.IsArray() {
				continue
			}
			childPath := joinPath(path, key)
			if key == "anyOf" || key == "oneOf" {
				paths[key] = append(paths[key], childPath)
			}
			for i, child := range children.Array() {
				walk(child, joinPath(childPath, strconv.Itoa(i)))
			}
		}
		for _, key := range falseSchemaMapKeywords {
			schema.Get(key).ForEach(func(name, child gjson.Result) bool {
				walk(child, joinPath(joinPath(path, key), escapeGJSONPathKey(name.String())))
				return true
			})
		}
		for _, key := range falseSchemaSingleKeywords {
			child := schema.Get(key)
			childPath := joinPath(path, key)
			if child.IsArray() {
				for i, item := range child.Array() {
					walk(item, joinPath(childPath, strconv.Itoa(i)))
				}
			} else {
				walk(child, childPath)
			}
		}
	}
	root := gjson.Parse(jsonStr)
	// The executor's cleanNestedSchema wrapper preserves placeholder behavior.
	if root.IsObject() && len(root.Map()) == 1 && root.Get("schema").IsObject() {
		walk(root.Get("schema"), "schema")
	} else {
		walk(root, "")
	}
	return paths
}
