package localmodels

import (
	"encoding/json/jsontext"
	"strings"
	"testing"
)

func optionTexts(t *testing.T, q Question) string {
	t.Helper()
	opts, err := Options(q)
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, len(opts))
	for i, o := range opts {
		texts[i] = o.Text()
	}
	return strings.Join(texts, " | ")
}

// Options must render as Julia 2's prompt.options_for does, or the models
// see different text than they were trained on.
func TestOptionsRenderLikeJulia(t *testing.T) {
	cases := map[string]struct {
		q    Question
		want string
	}{
		"choice keeps caller order": {Question{Type: TypeChoice, Criteria: jsontext.Value(`{"z":"Last letter","a":"First letter"}`)}, "z: Last letter | a: First letter"},
		"choice key alone":          {Question{Type: TypeChoice, Criteria: jsontext.Value(`{"red":"","blue":null,"green":"green"}`)}, "red | blue | green"},
		"choice object value":       {Question{Type: TypeChoice, Criteria: jsontext.Value(`{"x":{"min":1}}`)}, `x: {"min":1}`},
		"noul defaults":             {Question{Type: TypeNoul}, "true: Yes | false: No"},
		"noul labels":               {Question{Type: TypeNoul, Criteria: jsontext.Value(`{"true":"Fraud","false":"Fine"}`)}, "true: Fraud | false: Fine"},
		"score levels":              {Question{Type: TypeScore, Criteria: jsontext.Value(`["Low","High"]`)}, "0: Low | 1: High"},
	}
	for name, tc := range cases {
		if got := optionTexts(t, tc.q); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestTextHelpers(t *testing.T) {
	if got := InstructionsText(jsontext.Value(`{"goal":"refund?","limit":50}`)); got != "goal: refund?\nlimit: 50" {
		t.Errorf("instructions = %q", got)
	}
	if got := StateText(jsontext.Value(`["first","second"]`)); got != "first\nsecond" {
		t.Errorf("state = %q", got)
	}
	if got := StateText(jsontext.Value(`{"a":1}`)); got != `{"a":1}` {
		t.Errorf("object state = %q", got)
	}
}

func TestQuestionsJSONKeepsOrder(t *testing.T) {
	out, err := questionsJSON([]Question{{Name: "b", Type: TypeNoul}, {Name: "a", Type: TypeScore, Criteria: jsontext.Value(`["x"]`)}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"b":{"type":"noul"},"a":{"type":"score","criteria":["x"]}}`; string(out) != want {
		t.Fatalf("got %s, want %s", out, want)
	}
}
