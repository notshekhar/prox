package systemone

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/MoeForge/moego/base64"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels"
)

// System One's asset limits.
const (
	maxAssetBytes  = 16 << 20 // one file, after decoding
	maxAssetsBytes = 32 << 20 // every file in a request
	maxRefBytes    = 1024     // one asset id
	maxRefsBytes   = 64 << 10 // every asset id
)

// Formats each kind of asset may use. Audio is prox's extension; System One
// only has images.
var assetFormats = map[localmodels.MediaKind][]string{
	localmodels.MediaImage: {"png", "jpg", "jpeg"},
	localmodels.MediaAudio: {"wav", "flac", "ogg"},
}

type assetSpec struct {
	Type       string `json:"type"`
	Format     string `json:"format"`
	DataBase64 string `json:"data_base64"`
	Part       string `json:"part"`
}

// statusError is a request System One refuses: an HTTP status and its detail.
type statusError struct {
	status int
	detail string
}

func badRequest(format string, args ...any) *statusError {
	return &statusError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func tooLarge(format string, args ...any) *statusError {
	return &statusError{http.StatusRequestEntityTooLarge, fmt.Sprintf(format, args...)}
}

// parseAssets reads the assets map. A spec naming a part takes that multipart
// file; otherwise it carries base64 data.
func parseAssets(raw jsontext.Value, files map[string][]byte) (map[string]localmodels.Asset, *statusError) {
	if len(raw) == 0 || raw.Kind() == 'n' {
		if len(files) > 0 {
			return nil, badRequest("the request has multipart files but no assets naming them")
		}
		return nil, nil
	}
	var specs map[string]assetSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, badRequest("assets: %v", err)
	}
	if err := checkReferenceIDs(specs); err != nil {
		return nil, err
	}
	assets := make(map[string]localmodels.Asset, len(specs))
	total := 0
	for _, id := range sortedKeys(specs) {
		a, err := buildAsset(id, specs[id], files)
		if err != nil {
			return nil, err
		}
		if total += len(a.Data); total > maxAssetsBytes {
			return nil, tooLarge("assets add up to more than %d MiB", maxAssetsBytes>>20)
		}
		assets[id] = a
	}
	if part := unclaimedPart(specs, files); part != "" {
		return nil, badRequest("multipart part %q is not named by any asset", part)
	}
	return assets, nil
}

// unclaimedPart finds a multipart file no asset names; System One refuses those.
func unclaimedPart(specs map[string]assetSpec, files map[string][]byte) string {
	claimed := map[string]bool{}
	for _, spec := range specs {
		claimed[spec.Part] = true
	}
	for _, name := range sortedKeys(files) {
		if !claimed[name] {
			return name
		}
	}
	return ""
}

func checkReferenceIDs(specs map[string]assetSpec) *statusError {
	total := 0
	for id := range specs {
		if strings.TrimSpace(id) == "" {
			return badRequest("an asset id cannot be empty")
		}
		if len(id) > maxRefBytes {
			return tooLarge("asset id %.40q… is longer than %d bytes", id, maxRefBytes)
		}
		total += len(id)
	}
	if total > maxRefsBytes {
		return tooLarge("asset ids add up to more than %d bytes", maxRefsBytes)
	}
	return nil
}

func buildAsset(id string, spec assetSpec, files map[string][]byte) (localmodels.Asset, *statusError) {
	kind := localmodels.MediaKind(spec.Type)
	if kind == "" {
		kind = localmodels.MediaImage
	}
	formats, ok := assetFormats[kind]
	if !ok {
		return localmodels.Asset{}, badRequest("asset %q: type must be \"image\" or \"audio\"", id)
	}
	data, err := assetData(id, spec, files)
	if err != nil {
		return localmodels.Asset{}, err
	}
	format := strings.ToLower(spec.Format)
	if format == "" && spec.Part != "" {
		format = sniffFormat(data)
	}
	if !slices.Contains(formats, format) {
		return localmodels.Asset{}, badRequest("asset %q: %s format must be one of %s", id, kind, strings.Join(formats, ", "))
	}
	if len(data) > maxAssetBytes {
		return localmodels.Asset{}, tooLarge("asset %q is larger than %d MiB", id, maxAssetBytes>>20)
	}
	if len(data) == 0 {
		return localmodels.Asset{}, badRequest("asset %q is empty", id)
	}
	return localmodels.Asset{Kind: kind, Format: format, Data: data}, nil
}

func assetData(id string, spec assetSpec, files map[string][]byte) ([]byte, *statusError) {
	if spec.Part != "" {
		data, ok := files[spec.Part]
		if !ok {
			return nil, badRequest("asset %q names part %q, which the request does not have", id, spec.Part)
		}
		return data, nil
	}
	if spec.DataBase64 == "" {
		return nil, badRequest("asset %q needs data_base64 or a multipart part", id)
	}
	if len(spec.DataBase64) > base64.StdEncoding.EncodedLen(maxAssetBytes) {
		return nil, tooLarge("asset %q is larger than %d MiB", id, maxAssetBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(spec.DataBase64)
	if err != nil {
		return nil, badRequest("asset %q: data_base64 is not standard padded base64", id)
	}
	return data, nil
}

// sniffFormat names a multipart file sent without a format by its first bytes.
func sniffFormat(data []byte) string {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "png"
	case len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff":
		return "jpg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE":
		return "wav"
	case len(data) >= 4 && string(data[:4]) == "fLaC":
		return "flac"
	case len(data) >= 4 && string(data[:4]) == "OggS":
		return "ogg"
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// unreferenced finds the first asset nothing in the request points at.
func unreferenced(req localmodels.Request) string {
	used := localmodels.FindReferences(req).Used
	for _, id := range sortedKeys(req.Assets) {
		if !used[id] {
			return id
		}
	}
	return ""
}
