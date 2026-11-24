package localmodels

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
)

// Question types every decision runtime understands.
const (
	TypeNoul   = "noul"
	TypeChoice = "choice"
	TypeScore  = "score"
)

// Request is one decision request: a state and questions in the caller's order.
type Request struct {
	State     jsontext.Value // a JSON string, object, or array of strings
	Questions []Question
}

// Question is one named question. Instructions and criteria stay raw JSON:
// they may be strings, objects or arrays, and a choice's option order matters.
type Question struct {
	Name         string         `json:"-"`
	Type         string         `json:"type"`
	Instructions jsontext.Value `json:"instructions,omitzero"`
	Criteria     jsontext.Value `json:"criteria,omitzero"`
}

// RawAnswer is an answer as a runtime returns it, before an API shapes it.
type RawAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// Result is a runtime's reply to one Request.
type Result struct {
	Answers     map[string]RawAnswer `json:"answers"`
	InputTokens int                  `json:"input_tokens"`
}

// Option is one answer a question offers, in the order the model sees them.
type Option struct {
	Key         string
	Description string
}

// Text is how the option is written for the model: "key: description", or the
// key alone when there is no description.
func (o Option) Text() string {
	if o.Description == "" || o.Description == o.Key {
		return o.Key
	}
	return o.Key + ": " + o.Description
}

// Options lists a question's answers the way Julia 2 renders them: yes/no is
// true then false, a score's levels are keyed 0, 1, 2…, and a choice keeps the
// caller's order.
func Options(q Question) ([]Option, error) {
	switch q.Type {
	case TypeNoul:
		return noulOptions(q.Criteria), nil
	case TypeScore:
		return scoreOptions(q.Criteria)
	case TypeChoice:
		return choiceOptions(q.Criteria)
	}
	return nil, fmt.Errorf("question %q has unsupported type %q", q.Name, q.Type)
}

func noulOptions(criteria jsontext.Value) []Option {
	labels := map[string]jsontext.Value{}
	if criteria.Kind() == '{' {
		_ = json.Unmarshal(criteria, &labels)
	}
	describe := func(key, fallback string) string {
		if text := valueText(labels[key]); text != "" {
			return text
		}
		return fallback
	}
	return []Option{{"true", describe("true", "Yes")}, {"false", describe("false", "No")}}
}

func scoreOptions(criteria jsontext.Value) ([]Option, error) {
	var levels []jsontext.Value
	if err := json.Unmarshal(criteria, &levels); err != nil {
		return nil, fmt.Errorf("score criteria must be an array: %w", err)
	}
	out := make([]Option, len(levels))
	for i, level := range levels {
		out[i] = Option{strconv.Itoa(i), valueText(level)}
	}
	return out, nil
}

func choiceOptions(criteria jsontext.Value) ([]Option, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(criteria))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return nil, fmt.Errorf("choice criteria must be an object")
	}
	var out []Option
	for dec.PeekKind() == '"' {
		key, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		name := key.String() // a token is only valid until the next read
		value, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		out = append(out, Option{name, valueText(value)})
	}
	return out, nil
}

// valueText is a criteria value as text: a string as itself, null as nothing,
// anything else as its JSON.
func valueText(v jsontext.Value) string {
	switch v.Kind() {
	case 0, 'n':
		return ""
	case '"':
		var s string
		_ = json.Unmarshal(v, &s)
		return s
	}
	return string(v)
}

// InstructionsText is a question's instructions as text; an object becomes
// "key: value" lines, as Julia 2 writes it.
func InstructionsText(v jsontext.Value) string {
	if v.Kind() != '{' {
		return valueText(v)
	}
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	_, _ = dec.ReadToken()
	var lines []string
	for dec.PeekKind() == '"' {
		key, err := dec.ReadToken()
		if err != nil {
			break
		}
		name := key.String()
		value, err := dec.ReadValue()
		if err != nil {
			break
		}
		lines = append(lines, name+": "+valueText(value))
	}
	return strings.Join(lines, "\n")
}

// StateText is the state as text: a string as itself, an array's strings one
// per line, an object as its JSON.
func StateText(v jsontext.Value) string {
	if v.Kind() != '[' {
		return valueText(v)
	}
	var items []string
	_ = json.Unmarshal(v, &items)
	return strings.Join(items, "\n")
}

// questionsJSON writes the questions as one JSON object in the caller's order;
// Go maps would shuffle it.
func questionsJSON(questions []Question) ([]byte, error) {
	out := []byte{'{'}
	for i, q := range questions {
		if i > 0 {
			out = append(out, ',')
		}
		key, err := jsontext.AppendQuote(nil, q.Name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(q)
		if err != nil {
			return nil, err
		}
		out = append(append(append(out, key...), ':'), value...)
	}
	return append(out, '}'), nil
}
