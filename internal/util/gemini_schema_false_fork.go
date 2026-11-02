package util

import (
	"iter"
	"strconv"
)

var falseSchemaMapKeywords = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}

var falseSchemaSingleKeywords = []string{
	"items", "additionalProperties", "not", "if", "then", "else", "contains", "propertyNames",
	"unevaluatedProperties", "unevaluatedItems", "contentSchema", "additionalItems",
}

var falseSchemaArrayKeywords = []string{"anyOf", "oneOf", "allOf", "prefixItems"}

// allFalseSchemaUnion distinguishes an unsatisfiable union from malformed or
// required-only branches that the compatibility cleaner may also filter out.
func allFalseSchemaUnion[T interface {
	Value() any
	IsObject() bool
	IsArray() bool
}](items []T) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if item.IsObject() || item.IsArray() {
			return false
		}
		if value, ok := item.Value().(bool); !ok || value {
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
	return containsFalseSchema(schema)
}

func containsFalseSchema(raw string) bool {
	schema := ParseGJSONBytesNoCopy([]byte(raw))
	if schema.Raw == "false" {
		return true
	}
	if !schema.IsObject() {
		return false
	}
	for _, key := range falseSchemaMapKeywords {
		for _, child := range forkJSONEntries(schema.Get(key)) {
			if containsFalseSchema(child.Raw) {
				return true
			}
		}
	}
	for _, key := range falseSchemaSingleKeywords {
		child := schema.Get(key)
		if key == "additionalProperties" && child.Raw == "false" {
			continue
		}
		if child.IsArray() {
			for _, item := range child.Array() {
				if containsFalseSchema(item.Raw) {
					return true
				}
			}
		} else if containsFalseSchema(child.Raw) {
			return true
		}
	}
	for _, key := range falseSchemaArrayKeywords {
		for _, child := range schema.Get(key).Array() {
			if containsFalseSchema(child.Raw) {
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
	var walk func(string, string)
	walk = func(raw, path string) {
		schema := ParseGJSONBytesNoCopy([]byte(raw))
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
				walk(child.Raw, joinPath(childPath, strconv.Itoa(i)))
			}
		}
		for _, key := range falseSchemaMapKeywords {
			for name, child := range forkJSONEntries(schema.Get(key)) {
				walk(child.Raw, joinPath(joinPath(path, key), escapeGJSONPathKey(name.String())))
			}
		}
		for _, key := range falseSchemaSingleKeywords {
			child := schema.Get(key)
			childPath := joinPath(path, key)
			if child.IsArray() {
				for i, item := range child.Array() {
					walk(item.Raw, joinPath(childPath, strconv.Itoa(i)))
				}
			} else {
				walk(child.Raw, childPath)
			}
		}
	}
	root := ParseGJSONBytesNoCopy([]byte(jsonStr))
	// The executor's cleanNestedSchema wrapper preserves placeholder behavior.
	if root.IsObject() && len(root.Map()) == 1 && root.Get("schema").IsObject() {
		walk(root.Get("schema").Raw, "schema")
	} else {
		walk(root.Raw, "")
	}
	return paths
}

// forkJSONEntries preserves parser iteration order and duplicate object keys.
// Both sides of the upstream JSON migration expose this same ForEach contract;
// a map conversion would silently discard later duplicate schema definitions.
func forkJSONEntries[T interface{ ForEach(func(T, T) bool) }](node T) iter.Seq2[T, T] {
	return func(yield func(T, T) bool) { node.ForEach(yield) }
}
