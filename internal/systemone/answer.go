package systemone

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"strconv"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels"
)

// Response is the body of a successful POST /v1/systemone.
type Response struct {
	Model   string        `json:"model"`
	Answers orderedObject `json:"answers"`
	Usage   Usage         `json:"usage"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"` // always 0: nothing is generated
}

type noulAnswer struct {
	Type string  `json:"type"`
	Noul float64 `json:"noul"`
}

type choiceAnswer struct {
	Type          string        `json:"type"`
	Choice        string        `json:"choice"`
	Confidence    float64       `json:"confidence"`
	Probabilities orderedObject `json:"probabilities"`
}

type scoreAnswer struct {
	Type          string        `json:"type"`
	Score         float64       `json:"score"`
	Confidence    float64       `json:"confidence"`
	Legend        orderedObject `json:"legend"`
	Probabilities orderedObject `json:"probabilities"`
}

// shapeAnswers turns the runtime's answers into System One's, in question order.
func shapeAnswers(questions []question, raw map[string]localmodels.RawAnswer) (orderedObject, error) {
	var answers orderedObject
	for _, q := range questions {
		a, ok := raw[q.Name]
		if !ok {
			return nil, fmt.Errorf("the model returned no answer for %q", q.Name)
		}
		value, err := shapeAnswer(q, a)
		if err != nil {
			return nil, err
		}
		answers = append(answers, member{q.Name, value})
	}
	return answers, nil
}

func shapeAnswer(q question, a localmodels.RawAnswer) (any, error) {
	switch q.Type {
	case typeNoul:
		if a.Noul == nil {
			return nil, fmt.Errorf("the model returned no probability for %q", q.Name)
		}
		return noulAnswer{Type: typeNoul, Noul: *a.Noul}, nil
	case typeChoice:
		keys := q.criteriaKeys()
		return choiceAnswer{Type: typeChoice, Choice: a.Choice, Confidence: peakConfidence(a.Probabilities), Probabilities: probabilities(keys, a.Probabilities)}, nil
	case typeScore:
		return shapeScore(q, a), nil
	}
	return nil, fmt.Errorf("question %q has unsupported type %q", q.Name, q.Type)
}

// shapeScore reports the probability-weighted level, as System One does, not
// the runtime's most likely level.
func shapeScore(q question, a localmodels.RawAnswer) scoreAnswer {
	levels := q.scoreLevels()
	keys := make([]string, len(levels))
	legend := make(orderedObject, len(levels))
	expected := 0.0
	for i, level := range levels {
		keys[i] = strconv.Itoa(i)
		legend[i] = member{keys[i], level}
		expected += float64(i) * a.Probabilities[keys[i]]
	}
	return scoreAnswer{Type: typeScore, Score: expected, Confidence: peakConfidence(a.Probabilities), Legend: legend, Probabilities: probabilities(keys, a.Probabilities)}
}

// peakConfidence is System One's confidence: (n·max − 1)/(n − 1), 0 when every
// option is equally likely and 1 when one option holds everything.
func peakConfidence(probs map[string]float64) float64 {
	n := float64(len(probs))
	if n <= 1 {
		return 1
	}
	peak := 0.0
	for _, p := range probs {
		peak = max(peak, p)
	}
	return max(0, (n*peak-1)/(n-1))
}

func probabilities(keys []string, probs map[string]float64) orderedObject {
	out := make(orderedObject, 0, len(keys))
	for _, k := range keys {
		out = append(out, member{k, probs[k]})
	}
	return out
}

// orderedObject is a JSON object that keeps its members in insertion order.
type orderedObject []member

type member struct {
	Key   string
	Value any
}

func (o orderedObject) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	for i, m := range o {
		if i > 0 {
			out = append(out, ',')
		}
		key, err := jsontext.AppendQuote(nil, m.Key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(m.Value)
		if err != nil {
			return nil, err
		}
		out = append(append(append(out, key...), ':'), value...)
	}
	return append(out, '}'), nil
}
