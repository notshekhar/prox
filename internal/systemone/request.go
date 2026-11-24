// Package systemone serves TypeSafe's System One decision API (the API Jev
// answers) from local decision models: POST /v1/systemone takes a state and
// named questions and returns typed answers with probabilities.
package systemone

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels"
)

// Question limits from the System One API.
const (
	maxChoiceOptions = 255
	maxScoreLevels   = 10
)

// typeBox asks for a bounding box in an image; local models have no images yet.
const typeBox = "box"

// Request is the body of POST /v1/systemone.
type Request struct {
	Model     string         `json:"model"`
	State     jsontext.Value `json:"state"`
	Questions jsontext.Value `json:"questions"`
	Assets    jsontext.Value `json:"assets,omitzero"`
}

// FieldError is one entry of a 422 response, in FastAPI's shape as System One returns it.
type FieldError struct {
	Loc  []any  `json:"loc"`
	Msg  string `json:"msg"`
	Type string `json:"type"`
}

// parsed is a request that passed validation.
type parsed struct {
	Model     string
	State     jsontext.Value
	Questions []localmodels.Question
	Assets    jsontext.Value // parsed separately: multipart files may fill it
}

// parseRequest decodes and validates a body, returning every problem at once
// the way System One does.
func parseRequest(body []byte) (parsed, []FieldError) {
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return parsed{}, []FieldError{{Loc: []any{"body"}, Msg: "JSON decode error: " + err.Error(), Type: "json_invalid"}}
	}
	var errs []FieldError
	if req.Model == "" {
		errs = append(errs, missing("model"))
	}
	state, stateErrs := normaliseState(req.State)
	errs = append(errs, stateErrs...)
	questions, questionErrs := parseQuestions(req.Questions)
	errs = append(errs, questionErrs...)
	return parsed{Model: req.Model, State: state, Questions: questions, Assets: req.Assets}, errs
}

func missing(field string) FieldError {
	return FieldError{Loc: []any{"body", field}, Msg: "Field required", Type: "missing"}
}

// normaliseState accepts a string, an object or an array. Array items that
// are not strings become JSON text, so a state can only ever carry text: the
// runtime reads typed items such as {"type":"image","image":"<path>"} as files.
func normaliseState(raw jsontext.Value) (jsontext.Value, []FieldError) {
	if len(raw) == 0 || raw.Kind() == 'n' {
		return nil, []FieldError{missing("state")}
	}
	switch raw.Kind() {
	case '"', '{':
		return raw, nil
	case '[':
		return flattenArray(raw)
	}
	return nil, []FieldError{{Loc: []any{"body", "state"}, Msg: "Input should be a valid string, object or array", Type: "state_type"}}
}

func flattenArray(raw jsontext.Value) (jsontext.Value, []FieldError) {
	var items []jsontext.Value
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, []FieldError{{Loc: []any{"body", "state"}, Msg: err.Error(), Type: "state_type"}}
	}
	texts := make([]string, len(items))
	for i, item := range items {
		if item.Kind() == '"' {
			_ = json.Unmarshal(item, &texts[i])
			continue
		}
		texts[i] = string(item)
	}
	out, err := json.Marshal(texts)
	if err != nil {
		return nil, []FieldError{{Loc: []any{"body", "state"}, Msg: err.Error(), Type: "state_type"}}
	}
	return out, nil
}

// parseQuestions walks the questions object token by token so the caller's
// order survives; Go maps would shuffle it.
func parseQuestions(raw jsontext.Value) ([]localmodels.Question, []FieldError) {
	if len(raw) == 0 || raw.Kind() == 'n' {
		return nil, []FieldError{missing("questions")}
	}
	if raw.Kind() != '{' {
		return nil, []FieldError{{Loc: []any{"body", "questions"}, Msg: "Input should be a valid object", Type: "dict_type"}}
	}
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.ReadToken(); err != nil {
		return nil, []FieldError{{Loc: []any{"body", "questions"}, Msg: err.Error(), Type: "dict_type"}}
	}
	var questions []localmodels.Question
	var errs []FieldError
	for dec.PeekKind() != '}' {
		q, qErrs, err := readQuestion(dec)
		if err != nil {
			return nil, append(errs, FieldError{Loc: []any{"body", "questions"}, Msg: err.Error(), Type: "dict_type"})
		}
		questions, errs = append(questions, q), append(errs, qErrs...)
	}
	if len(questions) == 0 {
		errs = append(errs, FieldError{Loc: []any{"body", "questions"}, Msg: "Object should have at least 1 property", Type: "too_short"})
	}
	return questions, errs
}

func readQuestion(dec *jsontext.Decoder) (localmodels.Question, []FieldError, error) {
	// A token is only valid until the next read, so take the name out first.
	nameToken, err := dec.ReadToken()
	if err != nil {
		return localmodels.Question{}, nil, err
	}
	q := localmodels.Question{Name: nameToken.String()}
	value, err := dec.ReadValue()
	if err != nil {
		return localmodels.Question{}, nil, err
	}
	if value.Kind() != '{' {
		return q, []FieldError{{Loc: questionLoc(q.Name), Msg: "Input should be a valid object", Type: "dict_type"}}, nil
	}
	if err = json.Unmarshal(value, &q); err != nil {
		return q, []FieldError{{Loc: questionLoc(q.Name), Msg: err.Error(), Type: "dict_type"}}, nil
	}
	return q, validateQuestion(q), nil
}

func questionLoc(name string, more ...any) []any {
	return append([]any{"body", "questions", name}, more...)
}

func validateQuestion(q localmodels.Question) []FieldError {
	switch q.Type {
	case localmodels.TypeNoul, typeBox:
		return nil
	case localmodels.TypeChoice:
		return validateCount(q, '{', "object", 1, maxChoiceOptions)
	case localmodels.TypeScore:
		return validateCount(q, '[', "array", 1, maxScoreLevels)
	case "":
		return []FieldError{{Loc: questionLoc(q.Name, "type"), Msg: "Field required", Type: "missing"}}
	}
	return []FieldError{{Loc: questionLoc(q.Name, "type"), Msg: "Input should be 'noul', 'choice', 'score' or 'box'", Type: "literal_error"}}
}

// validateCount checks that criteria is an object or array of min..max entries.
func validateCount(q localmodels.Question, kind jsontext.Kind, kindName string, min, max int) []FieldError {
	loc := questionLoc(q.Name, "criteria")
	if len(q.Criteria) == 0 {
		return []FieldError{{Loc: loc, Msg: "Field required", Type: "missing"}}
	}
	if q.Criteria.Kind() != kind {
		return []FieldError{{Loc: loc, Msg: "Input should be a valid " + kindName, Type: kindName + "_type"}}
	}
	n, err := countEntries(q.Criteria)
	if err != nil {
		return []FieldError{{Loc: loc, Msg: err.Error(), Type: kindName + "_type"}}
	}
	if n < min || n > max {
		return []FieldError{{Loc: loc, Msg: fmt.Sprintf("Should have %d to %d entries, not %d", min, max, n), Type: "length_error"}}
	}
	return nil
}

func countEntries(raw jsontext.Value) (int, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.ReadToken(); err != nil {
		return 0, err
	}
	n := 0
	for dec.PeekKind() != '}' && dec.PeekKind() != ']' {
		if raw.Kind() == '{' {
			if _, err := dec.ReadToken(); err != nil {
				return 0, err
			}
		}
		if err := dec.SkipValue(); err != nil {
			return 0, err
		}
		n++
	}
	if _, err := dec.ReadToken(); err != nil && err != io.EOF {
		return 0, err
	}
	return n, nil
}

// scoreLevels lists a score question's level descriptions, level 0 first.
func scoreLevels(q localmodels.Question) []jsontext.Value {
	var levels []jsontext.Value
	_ = json.Unmarshal(q.Criteria, &levels)
	return levels
}
