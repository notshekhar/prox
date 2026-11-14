package htmlsanitize

import (
	"bytes"
	json "encoding/json/v2"
	"html"
	"mime"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/jsoncompat"
)

// String escapes text before it is returned to browser-facing management clients.
func String(value string) string {
	return html.EscapeString(value)
}

// Strings escapes each string in values while preserving order.
func Strings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, String(value))
	}
	return out
}

// JSONBody escapes all string values in a JSON document.
func JSONBody(body []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return body, false
	}

	var value any
	if errDecode := json.Unmarshal(trimmed, &value, jsoncompat.DecodeUseNumber); errDecode != nil {
		return body, false
	}
	out, errEncode := json.Marshal(JSONValue(value), jsoncompat.EncodeNoHTMLEscape)
	if errEncode != nil {
		return body, false
	}
	return out, true
}

// JSONBodyIfLikely escapes JSON bodies when the content type or body shape indicates JSON.
func JSONBodyIfLikely(body []byte, contentType string) ([]byte, bool) {
	if IsJSONContentType(contentType) || LooksLikeJSON(body) {
		return JSONBody(body)
	}
	return body, false
}

// JSONValue recursively escapes string values in JSON-compatible data.
func JSONValue(value any) any {
	switch typed := value.(type) {
	case string:
		return String(typed)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = JSONValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = JSONValue(item)
		}
		return out
	default:
		return value
	}
}

// IsJSONContentType reports whether contentType is application/json or a +json type.
func IsJSONContentType(contentType string) bool {
	mediaType, _, errParse := mime.ParseMediaType(strings.TrimSpace(contentType))
	if errParse != nil {
		mediaType = strings.TrimSpace(contentType)
	}
	mediaType = strings.ToLower(mediaType)
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// LooksLikeJSON reports whether body starts with an object or array JSON marker.
func LooksLikeJSON(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	return trimmed[0] == '{' || trimmed[0] == '['
}
