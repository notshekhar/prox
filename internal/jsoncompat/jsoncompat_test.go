package jsoncompat

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"testing"
	"time"
)

func TestEncodeKeepsV1Shape(t *testing.T) {
	type payload struct {
		Tools   []string       `json:"tools"`
		Extra   map[string]any `json:"extra"`
		Text    string         `json:"text"`
		Timeout time.Duration  `json:"timeout"`
	}
	out, err := json.Marshal(payload{Text: "a\xffb <tag>", Timeout: 2 * time.Second}, Encode)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"tools":null,"extra":null,"text":"a` + "\uFFFD" + `b \u003ctag\u003e","timeout":2000000000}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}

	raw := struct {
		Body jsontext.Value `json:"body"`
	}{Body: jsontext.Value(`{"a":1,"a":2}`)}
	if out, err = json.Marshal(raw, Encode); err != nil || string(out) != `{"body":{"a":1,"a":2}}` {
		t.Fatalf("raw value with duplicate names: %s %v", out, err)
	}

	for range 20 {
		out, err = json.Marshal(map[string]int{"c": 3, "a": 1, "b": 2}, Encode)
		if err != nil || string(out) != `{"a":1,"b":2,"c":3}` {
			t.Fatalf("map keys not sorted: %s %v", out, err)
		}
	}
}

func TestEncodeNoHTMLEscapeKeepsLineSeparatorsEscaped(t *testing.T) {
	out, err := json.Marshal("<a&b>\u2028", EncodeNoHTMLEscape)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `"<a&b>\u2028"`; string(out) != want {
		t.Fatalf("got %s want %s", out, want)
	}
}

func TestDecodeAcceptsWhatV1Accepted(t *testing.T) {
	var req struct {
		Model   string        `json:"model"`
		Timeout time.Duration `json:"timeout"`
	}
	input := []byte(`{"MODEL":"m` + "\xff" + `","timeout":1,"timeout":1500}`)
	if err := json.Unmarshal(input, &req, Decode); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Model != "m\uFFFD" || req.Timeout != 1500 {
		t.Fatalf("decoded model = %q, timeout = %v", req.Model, req.Timeout)
	}

	req.Timeout = time.Minute
	if err := json.Unmarshal([]byte(`{"timeout":null}`), &req, Decode); err != nil || req.Timeout != time.Minute {
		t.Fatalf("null changed duration to %v, err = %v", req.Timeout, err)
	}
}

func TestDecodeUseNumberKeepsPrecision(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(`{"id":12345678901234567890,"list":[1.50],"s":"x"}`), &v, DecodeUseNumber); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v["id"] != Number("12345678901234567890") || v["s"] != "x" {
		t.Fatalf("decoded %#v", v)
	}
	out, err := json.Marshal(v, Encode)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"id":12345678901234567890,"list":[1.50],"s":"x"}`; string(out) != want {
		t.Fatalf("got %s want %s", out, want)
	}
	if _, err = json.Marshal(Number("1x")); err == nil {
		t.Fatal("invalid number literal encoded without error")
	}
}
