package systemone

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels"
)

// fakeEngine records what reaches the model and answers with fixed probabilities.
type fakeEngine struct {
	got     localmodels.Request
	answers map[string]localmodels.RawAnswer
	err     error
	resolve error
}

func (f *fakeEngine) Resolve(name string) (localmodels.Model, error) {
	if f.resolve != nil {
		return localmodels.Model{}, f.resolve
	}
	m, _ := localmodels.Find("julia-2-flash")
	return m, nil
}

func (f *fakeEngine) Decide(_ context.Context, _ localmodels.Model, req localmodels.Request) (localmodels.Result, error) {
	f.got = req
	return localmodels.Result{Answers: f.answers, InputTokens: 42}, f.err
}

func (f *fakeEngine) Statuses() []localmodels.Status {
	m, _ := localmodels.Find("julia-2-flash")
	return []localmodels.Status{{Model: m, Installed: true}}
}

func ptr[T any](v T) *T { return &v }

func serve(t *testing.T, engine Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandler(engine)
	r.POST("/v1/systemone", h.Decide)
	r.GET("/v1/systemone/models", h.Models)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

const fullRequest = `{
  "model": "julia-2-flash",
  "state": "Help! My payouts have been failing for 3 days.",
  "questions": {
    "is_urgent": {"type": "noul", "instructions": "Does this convey urgency?"},
    "department": {"type": "choice", "instructions": "Route it", "criteria": {"technical": "Bugs", "billing": "Payments", "sales": "Pricing"}},
    "priority": {"type": "score", "criteria": ["Can wait", "This week", "Today"]}
  }
}`

func fullAnswers() map[string]localmodels.RawAnswer {
	return map[string]localmodels.RawAnswer{
		"is_urgent":  {Type: "noul", Noul: ptr(0.85), Confidence: 0.7},
		"department": {Type: "choice", Choice: "billing", Confidence: 0.9, Probabilities: map[string]float64{"technical": 0.04, "billing": 0.94, "sales": 0.02}},
		"priority":   {Type: "score", Score: ptr(2), ExpectedLevel: ptr(1.87), Confidence: 0.78, Probabilities: map[string]float64{"0": 0.01, "1": 0.10, "2": 0.89}},
	}
}

func TestDecideAnswersInSystemOneShape(t *testing.T) {
	engine := &fakeEngine{answers: fullAnswers()}
	rec := serve(t, engine, http.MethodPost, "/v1/systemone", fullRequest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	want := `{"model":"julia-2-flash","answers":{` +
		`"is_urgent":{"type":"noul","noul":0.85},` +
		`"department":{"type":"choice","choice":"billing","confidence":0.91,"probabilities":{"technical":0.04,"billing":0.94,"sales":0.02}},` +
		`"priority":{"type":"score","score":1.88,"confidence":0.835,"legend":{"0":"Can wait","1":"This week","2":"Today"},"probabilities":{"0":0.01,"1":0.1,"2":0.89}}` +
		`},"usage":{"input_tokens":42,"output_tokens":0}}`
	if got := roundFloats(t, rec.Body.String()); got != roundFloats(t, want) {
		t.Fatalf("response\n got %s\nwant %s", got, roundFloats(t, want))
	}
}

// roundFloats re-encodes JSON with floats rounded to 3 places, keeping member order.
func roundFloats(t *testing.T, s string) string {
	t.Helper()
	var out strings.Builder
	num := ""
	flush := func() {
		if num == "" {
			return
		}
		var f float64
		if err := json.Unmarshal([]byte(num), &f); err != nil {
			t.Fatalf("bad number %q", num)
		}
		b, _ := json.Marshal(float64(int(f*1000+0.5)) / 1000)
		out.Write(b)
		num = ""
	}
	inString := false
	for _, r := range s {
		switch {
		case r == '"':
			flush()
			inString = !inString
			out.WriteRune(r)
		case !inString && (r == '.' || r == '-' || (r >= '0' && r <= '9')):
			num += string(r)
		default:
			flush()
			out.WriteRune(r)
		}
	}
	flush()
	return out.String()
}

func TestDecideKeepsQuestionAndOptionOrderForTheModel(t *testing.T) {
	engine := &fakeEngine{answers: fullAnswers()}
	serve(t, engine, http.MethodPost, "/v1/systemone", fullRequest)
	got := string(engine.got.Questions)
	order := []string{`"is_urgent"`, `"department"`, `"technical"`, `"billing"`, `"sales"`, `"priority"`}
	last := -1
	for _, key := range order {
		at := strings.Index(got, key)
		if at < last {
			t.Fatalf("%s moved out of order in %s", key, got)
		}
		last = at
	}
}

// Typed state items such as {"type":"image","image":"/etc/passwd"} are read
// as files by the runtime; they must reach it as plain text.
func TestDecideStateArrayCarriesOnlyText(t *testing.T) {
	engine := &fakeEngine{answers: map[string]localmodels.RawAnswer{"q": {Type: "noul", Noul: ptr(0.5)}}}
	body := `{"model":"julia-2-flash","state":["first",{"type":"image","image":"/etc/passwd"},3],"questions":{"q":{"type":"noul"}}}`
	rec := serve(t, engine, http.MethodPost, "/v1/systemone", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var items []any
	if err := json.Unmarshal(engine.got.State, &items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			t.Fatalf("state item %v reached the model as %T", item, item)
		}
	}
}

func TestDecideValidation(t *testing.T) {
	cases := map[string]struct{ body, loc string }{
		"no model":        {`{"state":"x","questions":{"q":{"type":"noul"}}}`, `["body","model"]`},
		"no state":        {`{"model":"m","questions":{"q":{"type":"noul"}}}`, `["body","state"]`},
		"no questions":    {`{"model":"m","state":"x","questions":{}}`, `["body","questions"]`},
		"bad type":        {`{"model":"m","state":"x","questions":{"q":{"type":"rank"}}}`, `["body","questions","q","type"]`},
		"choice no crit":  {`{"model":"m","state":"x","questions":{"q":{"type":"choice"}}}`, `["body","questions","q","criteria"]`},
		"score as object": {`{"model":"m","state":"x","questions":{"q":{"type":"score","criteria":{"a":"b"}}}}`, `["body","questions","q","criteria"]`},
		"score too long":  {`{"model":"m","state":"x","questions":{"q":{"type":"score","criteria":[1,2,3,4,5,6,7,8,9,10,11]}}}`, `["body","questions","q","criteria"]`},
		"not json":        {`{`, `["body"]`},
	}
	for name, tc := range cases {
		rec := serve(t, &fakeEngine{}, http.MethodPost, "/v1/systemone", tc.body)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"loc":`+tc.loc) {
			t.Errorf("%s: %d %s, want 422 at %s", name, rec.Code, rec.Body, tc.loc)
		}
	}
}

func TestDecideRejectsWhatLocalModelsCannotDo(t *testing.T) {
	for name, body := range map[string]string{
		"box":    `{"model":"m","state":"x","questions":{"q":{"type":"box"}}}`,
		"assets": `{"model":"m","state":"x","assets":{"img":{"type":"image","format":"png","data_base64":"AA=="}},"questions":{"q":{"type":"noul"}}}`,
	} {
		rec := serve(t, &fakeEngine{}, http.MethodPost, "/v1/systemone", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"detail":"`) {
			t.Errorf("%s: %d %s, want 400 with a detail", name, rec.Code, rec.Body)
		}
	}
}

func TestDecideErrors(t *testing.T) {
	m, _ := localmodels.Find("julia-2-flash")
	cases := map[string]struct {
		engine *fakeEngine
		status int
		detail string
	}{
		"not installed": {&fakeEngine{resolve: &localmodels.NotInstalledError{Model: m}}, http.StatusBadRequest, "prox local install julia-2-flash"},
		"bad request":   {&fakeEngine{err: &localmodels.RequestError{Message: "ValueError: options exceed 255"}}, http.StatusBadRequest, "options exceed"},
		"model down":    {&fakeEngine{err: context.DeadlineExceeded}, http.StatusServiceUnavailable, "deadline"},
	}
	for name, tc := range cases {
		rec := serve(t, tc.engine, http.MethodPost, "/v1/systemone", `{"model":"julia-2-flash","state":"x","questions":{"q":{"type":"noul"}}}`)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.detail) {
			t.Errorf("%s: %d %s, want %d mentioning %q", name, rec.Code, rec.Body, tc.status, tc.detail)
		}
	}
}

func TestModelsListsInstalledState(t *testing.T) {
	rec := serve(t, &fakeEngine{}, http.MethodGet, "/v1/systemone/models", "")
	if !strings.Contains(rec.Body.String(), `"name":"julia-2-flash"`) || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("models = %s", rec.Body)
	}
}

func TestPeakConfidence(t *testing.T) {
	if got := peakConfidence(map[string]float64{"a": 1.0 / 3, "b": 1.0 / 3, "c": 1.0 / 3}); got > 1e-9 {
		t.Fatalf("uniform = %v, want 0", got)
	}
	if got := peakConfidence(map[string]float64{"a": 1, "b": 0}); got != 1 {
		t.Fatalf("certain = %v, want 1", got)
	}
}
