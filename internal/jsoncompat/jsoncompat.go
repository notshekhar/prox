// Package jsoncompat holds the encoding/json/v2 options that keep this proxy's
// JSON behaviour the same as it was under encoding/json (v1).
//
// The proxy decodes client requests, upstream responses and user-edited files,
// and re-encodes payloads that upstream providers and the dashboard read. v2's
// stricter defaults would reject requests v1 accepted and change bytes v1
// produced, so every encode and decode outside tests passes Encode or Decode.
package jsoncompat

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
)

// Encode marshals the way v1 did, byte for byte:
//   - nil slices and maps encode as null, not [] and {}; providers treat an
//     empty "tools": [] differently from a missing or null one.
//   - map keys are sorted, and <, >, &, U+2028 and U+2029 are escaped as
//     \u003c and so on; session hashes, signatures, cache keys and upstream
//     prompt caches are computed over these bytes, and nodes of one cluster
//     may run different builds.
//   - invalid UTF-8 becomes U+FFFD instead of failing; model output and client
//     text can carry it, and v1 never failed a request over it.
//   - duplicate names inside raw values pass through; jsontext.Value fields
//     carry client JSON verbatim.
//   - time.Duration encodes as integer nanoseconds.
var Encode = json.JoinOptions(
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
	json.Deterministic(true),
	jsontext.EscapeForHTML(true),
	jsontext.EscapeForJS(true),
	jsontext.AllowInvalidUTF8(true),
	jsontext.AllowDuplicateNames(true),
	json.WithMarshalers(durationMarshalers),
)

// EncodeNoHTMLEscape is Encode without the HTML escaping, matching a v1
// Encoder after SetEscapeHTML(false). U+2028 and U+2029 stay escaped, as v1
// always escaped them.
var EncodeNoHTMLEscape = json.JoinOptions(Encode, jsontext.EscapeForHTML(false))

// Decode unmarshals with v1's tolerance for input the proxy does not author:
// object names match struct fields case-insensitively, duplicate names keep
// the last value, invalid UTF-8 becomes U+FFFD, and time.Duration decodes from
// integer nanoseconds.
//
// v2's case-insensitive match also ignores '-' and '_', which v1 did not. That
// only widens what is accepted.
var Decode = json.JoinOptions(
	json.MatchCaseInsensitiveNames(true),
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
	json.WithUnmarshalers(durationUnmarshalers),
)

// DecodeUseNumber is Decode plus v1's Decoder.UseNumber: numbers decoded into
// an interface value become Number instead of float64, so large integers keep
// their precision.
var DecodeUseNumber = json.JoinOptions(
	Decode,
	json.WithUnmarshalers(json.JoinUnmarshalers(durationUnmarshalers, numberUnmarshalers)),
)
