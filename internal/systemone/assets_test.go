package systemone

import (
	"bytes"
	json "encoding/json/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/MoeForge/moego/base64"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\nrest of a png")

func imageRequest(asset string) string {
	return `{"model":"julia-2-flash","state":{"door":"img-1"},"assets":{"img-1":` + asset + `},` +
		`"questions":{"q":{"type":"noul","instructions":"Is there a person at the door?"}}}`
}

func noulEngine() *fakeEngine {
	return &fakeEngine{answers: map[string]localmodels.RawAnswer{"q": {Type: "noul", Noul: ptr(0.7)}}}
}

// detailOf is the decoded detail message of an error response.
func detailOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Detail
}

func TestBase64AssetReachesTheModel(t *testing.T) {
	engine := noulEngine()
	asset := `{"type":"image","format":"png","data_base64":"` + base64.StdEncoding.EncodeToString(pngBytes) + `"}`
	rec := serve(t, engine, http.MethodPost, "/v1/systemone", imageRequest(asset))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := engine.got.Assets["img-1"]
	if got.Kind != localmodels.MediaImage || got.Format != "png" || !bytes.Equal(got.Data, pngBytes) {
		t.Fatalf("asset = %+v", got)
	}
}

func multipartBody(t *testing.T, request string, files map[string][]byte) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="request"`)
	header.Set("Content-Type", "application/json")
	part, _ := w.CreatePart(header)
	part.Write([]byte(request))
	for name, data := range files {
		fw, _ := w.CreateFormFile(name, name+".bin")
		fw.Write(data)
	}
	w.Close()
	return w.FormDataContentType(), &buf
}

func serveRaw(t *testing.T, engine Engine, contentType string, body *bytes.Buffer) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/systemone", NewHandler(engine).Decide)
	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestMultipartAssetReachesTheModel(t *testing.T) {
	engine := noulEngine()
	ct, body := multipartBody(t, imageRequest(`{"type":"image","part":"door-photo"}`), map[string][]byte{"door-photo": pngBytes})
	rec := serveRaw(t, engine, ct, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := engine.got.Assets["img-1"]; got.Format != "png" || !bytes.Equal(got.Data, pngBytes) {
		t.Fatalf("asset = %+v, want the part's bytes with a sniffed format", got)
	}
}

func TestAssetErrors(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	cases := map[string]struct {
		body   string
		status int
		detail string
	}{
		"bad format":   {imageRequest(`{"format":"gif","data_base64":"` + b64 + `"}`), 400, "format must be one of png"},
		"bad type":     {imageRequest(`{"type":"video","format":"png","data_base64":"` + b64 + `"}`), 400, `type must be "image" or "audio"`},
		"bad base64":   {imageRequest(`{"format":"png","data_base64":"not base64!"}`), 400, "base64"},
		"no data":      {imageRequest(`{"format":"png"}`), 400, "needs data_base64"},
		"missing part": {imageRequest(`{"part":"nope"}`), 400, `part "nope"`},
		"too large":    {imageRequest(`{"format":"png","data_base64":"` + strings.Repeat("A", base64.StdEncoding.EncodedLen(maxAssetBytes)+4) + `"}`), 413, "larger than"},
		"unreferenced": {`{"model":"m","state":"x","assets":{"img-9":{"format":"png","data_base64":"` + b64 + `"}},"questions":{"q":{"type":"noul"}}}`, 400, `"img-9" is not referenced`},
		"audio as wav": {imageRequest(`{"type":"audio","format":"mp3","data_base64":"` + b64 + `"}`), 400, "wav, flac, ogg"},
		"empty id":     {`{"model":"m","state":"x","assets":{" ":{"format":"png","data_base64":"` + b64 + `"}},"questions":{"q":{"type":"noul"}}}`, 400, "cannot be empty"},
	}
	for name, tc := range cases {
		rec := serve(t, noulEngine(), http.MethodPost, "/v1/systemone", tc.body)
		if rec.Code != tc.status || !strings.Contains(detailOf(t, rec), tc.detail) {
			t.Errorf("%s: %d %.200s, want %d mentioning %q", name, rec.Code, rec.Body, tc.status, tc.detail)
		}
	}
}

func TestMultipartErrors(t *testing.T) {
	cases := map[string]struct {
		request string
		files   map[string][]byte
		detail  string
	}{
		"unclaimed part":   {imageRequest(`{"part":"door-photo"}`), map[string][]byte{"door-photo": pngBytes, "extra": pngBytes}, `"extra" is not named`},
		"files, no assets": {`{"model":"m","state":"x","questions":{"q":{"type":"noul"}}}`, map[string][]byte{"door-photo": pngBytes}, "no assets"},
	}
	for name, tc := range cases {
		ct, body := multipartBody(t, tc.request, tc.files)
		rec := serveRaw(t, noulEngine(), ct, body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(detailOf(t, rec), tc.detail) {
			t.Errorf("%s: %d %s, want 400 mentioning %q", name, rec.Code, rec.Body, tc.detail)
		}
	}
	// The request part must come first.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("door-photo", "a.png")
	fw.Write(pngBytes)
	w.Close()
	if rec := serveRaw(t, noulEngine(), w.FormDataContentType(), &buf); !strings.Contains(rec.Body.String(), "first multipart part") {
		t.Errorf("request part second: %s", rec.Body)
	}
}

func TestBodyOverTheLimitIs413(t *testing.T) {
	big := bytes.NewBufferString(`{"model":"m","state":"` + strings.Repeat("a", maxBody) + `"}`)
	if rec := serveRaw(t, noulEngine(), "application/json", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d: %.200s", rec.Code, rec.Body)
	}
}
