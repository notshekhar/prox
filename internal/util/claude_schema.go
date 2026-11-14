package util

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/jsoncompat"
)

const emptyClaudeToolInputSchema = `{"type":"object","properties":{}}`

// NormalizeClaudeToolInputSchema makes a JSON Schema compatible with Claude's
// requirement that a tool input schema is an object without root-level unions.
func NormalizeClaudeToolInputSchema(schema []byte) []byte {
	var root map[string]jsontext.Value
	if len(schema) == 0 || json.Unmarshal(schema, &root, jsoncompat.Decode) != nil || root == nil {
		return []byte(emptyClaudeToolInputSchema)
	}

	properties := claudeSchemaObject(root["properties"])
	for _, unionName := range []string{"anyOf", "oneOf", "allOf"} {
		unionRaw, exists := root[unionName]
		if !exists {
			continue
		}
		delete(root, unionName)

		var branches []jsontext.Value
		if json.Unmarshal(unionRaw, &branches, jsoncompat.Decode) != nil {
			continue
		}
		for _, branchRaw := range branches {
			var branch map[string]jsontext.Value
			if json.Unmarshal(branchRaw, &branch, jsoncompat.Decode) != nil || !claudeSchemaCanBeObject(branch) {
				continue
			}
			for name, property := range claudeSchemaObject(branch["properties"]) {
				if _, exists = properties[name]; !exists {
					properties[name] = property
				}
			}
			if unionName == "allOf" {
				mergeClaudeSchemaRequired(root, branch["required"])
			}
		}
	}

	root["type"] = jsontext.Value(`"object"`)
	propertiesRaw, errMarshalProperties := json.Marshal(properties, jsoncompat.Encode)
	if errMarshalProperties != nil {
		return []byte(emptyClaudeToolInputSchema)
	}
	root["properties"] = propertiesRaw

	normalized, errMarshalRoot := json.Marshal(root, jsoncompat.Encode)
	if errMarshalRoot != nil {
		return []byte(emptyClaudeToolInputSchema)
	}
	return normalized
}

func claudeSchemaObject(raw jsontext.Value) map[string]jsontext.Value {
	object := make(map[string]jsontext.Value)
	if len(raw) == 0 {
		return object
	}
	if errUnmarshal := json.Unmarshal(raw, &object, jsoncompat.Decode); errUnmarshal != nil || object == nil {
		return make(map[string]jsontext.Value)
	}
	return object
}

func claudeSchemaCanBeObject(schema map[string]jsontext.Value) bool {
	typeRaw, exists := schema["type"]
	if !exists {
		return true
	}

	var schemaType string
	if json.Unmarshal(typeRaw, &schemaType, jsoncompat.Decode) == nil {
		return schemaType == "object"
	}

	var schemaTypes []string
	if json.Unmarshal(typeRaw, &schemaTypes, jsoncompat.Decode) != nil {
		return false
	}
	for _, candidate := range schemaTypes {
		if candidate == "object" {
			return true
		}
	}
	return false
}

func mergeClaudeSchemaRequired(root map[string]jsontext.Value, branchRequired jsontext.Value) {
	var required []string
	if rootRequired, exists := root["required"]; exists {
		if errUnmarshal := json.Unmarshal(rootRequired, &required, jsoncompat.Decode); errUnmarshal != nil {
			required = nil
		}
	}

	var branchNames []string
	if json.Unmarshal(branchRequired, &branchNames, jsoncompat.Decode) != nil {
		return
	}

	seen := make(map[string]struct{}, len(required)+len(branchNames))
	for _, name := range required {
		seen[name] = struct{}{}
	}
	for _, name := range branchNames {
		if _, exists := seen[name]; exists {
			continue
		}
		required = append(required, name)
		seen[name] = struct{}{}
	}
	if len(required) == 0 {
		return
	}
	requiredRaw, errMarshal := json.Marshal(required, jsoncompat.Encode)
	if errMarshal == nil {
		root["required"] = requiredRaw
	}
}

// HasUnsupportedUnicodePropertyEscape reports whether a regular expression string
// contains a construct that strict upstream JSON Schema validators reject when
// they compile "pattern" values on the way to a provider.
//
// Two constructs are known to fail, both of which Anthropic's own API accepts
// verbatim (it does not validate pattern values), so they only surface after the
// request is translated or forwarded:
//
//   - Unicode property escapes (\p{...} or \P{...}): Python's built-in re module,
//     and validators relying on it, fail with "bad escape \p".
//   - The octal NUL escape (\0): validators that re-serialize or normalize the
//     schema reject it with "is not a 'regex'", while the equivalent \x00
//     spelling is accepted. Claude Code's built-in Artifact tool emits \0 in its
//     NUL guards (e.g. "^[^\0]*$" on the upload_asset file_paths item).
//
// Dropping the attribute is safe: the client validates its own input locally, so
// removing an upstream-side pattern constraint does not make a call succeed that
// would otherwise have been rejected by the tool.
func HasUnsupportedUnicodePropertyEscape(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '\\' {
			continue
		}
		if i+1 >= len(pattern) {
			break
		}
		next := pattern[i+1]
		if (next == 'p' || next == 'P') && i+2 < len(pattern) && pattern[i+2] == '{' {
			return true
		}
		if next == '0' {
			return true
		}
		i++ // skip the escaped character (including escaped backslash)
	}
	return false
}

// SchemaMapKeywords lists JSON Schema keywords whose values are maps of subschemas.
var SchemaMapKeywords = [...]string{
	"properties",
	"$defs",
	"definitions",
	"patternProperties",
	"dependentSchemas",
	"dependencies",
}

// SchemaValueKeywords lists JSON Schema keywords with a single nested subschema or a slice of subschemas.
var SchemaValueKeywords = [...]string{
	"items",
	"prefixItems",
	"contains",
	"additionalProperties",
	"propertyNames",
	"unevaluatedProperties",
	"unevaluatedItems",
	"additionalItems",
	"contentSchema",
	"anyOf",
	"oneOf",
	"allOf",
	"not",
	"if",
	"then",
	"else",
}
