package localmodels

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"strings"

	"github.com/MoeForge/moego/base64"
)

// MediaKind is what an asset holds.
type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaAudio MediaKind = "audio" // prox's extension: System One only has images
)

// Asset is one image or audio file a request carries. The request refers to it
// by its id: a string anywhere in the state or a question's text.
type Asset struct {
	Kind   MediaKind
	Format string // png, jpg or jpeg; wav, flac or ogg
	Data   []byte
}

// MIME is the asset's media type, for data URLs.
func (a Asset) MIME() string {
	switch a.Format {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "wav":
		return "audio/wav"
	case "flac":
		return "audio/flac"
	case "ogg":
		return "audio/ogg"
	}
	return "application/octet-stream"
}

// DataURL is the asset inline. Runtimes are given data, never a path: the
// Julia runtime would open a path it was handed as a local file.
func (a Asset) DataURL() string {
	return "data:" + a.MIME() + ";base64," + base64.StdEncoding.EncodeToString(a.Data)
}

// References says where each asset id appears in a request.
type References struct {
	StateWhole []string        // ids that are a whole string value in the state, in order of appearance
	Elsewhere  map[string]bool // ids inside longer state text, instructions or criteria
	Used       map[string]bool
}

// FindReferences scans the state and every question's instructions and
// criteria values for asset ids. Object keys never count, as in System One.
func FindReferences(req Request) References {
	refs := References{Elsewhere: map[string]bool{}, Used: map[string]bool{}}
	if len(req.Assets) == 0 {
		return refs
	}
	eachString(req.State, func(s string) {
		if _, ok := req.Assets[s]; ok {
			refs.StateWhole = append(refs.StateWhole, s)
			refs.Used[s] = true
			return
		}
		refs.markInside(s, req.Assets)
	})
	for _, q := range req.Questions {
		for _, v := range []jsontext.Value{q.Instructions, q.Criteria} {
			eachString(v, func(s string) {
				if _, ok := req.Assets[s]; ok {
					refs.Elsewhere[s], refs.Used[s] = true, true
					return
				}
				refs.markInside(s, req.Assets)
			})
		}
	}
	return refs
}

func (r *References) markInside(s string, assets map[string]Asset) {
	for id := range assets {
		if strings.Contains(s, id) {
			r.Elsewhere[id], r.Used[id] = true, true
		}
	}
}

// eachString calls fn for every string value in a JSON document, skipping
// object keys.
func eachString(v jsontext.Value, fn func(string)) {
	if len(v) == 0 {
		return
	}
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return
		}
		if tok.Kind() != '"' {
			continue
		}
		s := tok.String()
		if isObjectKey(dec) {
			continue
		}
		fn(s)
	}
}

// isObjectKey reports whether the string just read is a member name: inside an
// object, names sit at even positions.
func isObjectKey(dec *jsontext.Decoder) bool {
	kind, length := dec.StackIndex(dec.StackDepth())
	return kind == '{' && length%2 == 1
}

// ReplaceWholeReferences rewrites state strings that are asset ids with a
// label, e.g. "[image 1]", numbered per kind in order of appearance. It works
// token by token so the caller's key order survives.
func ReplaceWholeReferences(state jsontext.Value, assets map[string]Asset) (jsontext.Value, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(state))
	var out bytes.Buffer
	enc := jsontext.NewEncoder(&out)
	label := mediaLabeller(assets)
	for {
		tok, err := dec.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if tok.Kind() == '"' && !isObjectKey(dec) {
			tok = jsontext.String(label(tok.String()))
		}
		if err = enc.WriteToken(tok); err != nil {
			return nil, err
		}
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

// mediaLabeller names each asset id once: "[image 1]", "[audio 1]", ...
func mediaLabeller(assets map[string]Asset) func(string) string {
	counts := map[MediaKind]int{}
	labels := map[string]string{}
	return func(s string) string {
		a, ok := assets[s]
		if !ok {
			return s
		}
		if l, seen := labels[s]; seen {
			return l
		}
		counts[a.Kind]++
		labels[s] = fmt.Sprintf("[%s %d]", a.Kind, counts[a.Kind])
		return labels[s]
	}
}
