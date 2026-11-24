package localmodels

import (
	"context"
	"fmt"
	"io"
	"math"
	"path/filepath"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels/nano"
)

// nanoTextWeights is the text model inside the Julia 2 Nano repository.
const nanoTextWeights = "text/nano_text.bin"

// nanoRuntime runs Julia 2 Nano inside prox: a pure-Go port of its C program,
// so there is no Python, compiler or process to manage.
type nanoRuntime struct{}

var nanoTextRuntime = &nanoRuntime{}

func (*nanoRuntime) Name() string { return "nano" }

func (*nanoRuntime) ready(Layout, Model) bool { return true }

func (*nanoRuntime) install(ctx context.Context, in *Installer, log io.Writer, m Model) error {
	in.report(StepDownload)
	return in.fetchVerified(ctx, log, m, m.Files)
}

func (*nanoRuntime) start(_ context.Context, l Layout, m Model) (session, error) {
	model, err := nano.Load(filepath.Join(l.ModelDir(m), filepath.FromSlash(nanoTextWeights)))
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", m.ID, err)
	}
	return &nanoSession{model: model}, nil
}

// nanoSession is a loaded Nano model. It is read-only, so requests share it
// without locking.
type nanoSession struct{ model *nano.Model }

var nanoTypes = map[string]nano.QuestionType{TypeChoice: nano.Choice, TypeNoul: nano.YesNo, TypeScore: nano.Score}

func (s *nanoSession) decide(ctx context.Context, req Request) (Result, error) {
	state := StateText(req.State)
	res := Result{Answers: make(map[string]RawAnswer, len(req.Questions))}
	for _, q := range req.Questions {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		answer, tokens, err := s.answer(state, q)
		if err != nil {
			return Result{}, &RequestError{Message: err.Error()}
		}
		res.Answers[q.Name] = answer
		res.InputTokens += tokens
	}
	return res, nil
}

func (s *nanoSession) answer(state string, q Question) (RawAnswer, int, error) {
	opts, err := Options(q)
	if err != nil {
		return RawAnswer{}, 0, err
	}
	texts := make([]string, len(opts))
	for i, o := range opts {
		texts[i] = o.Text()
	}
	scored := s.model.Score(nano.Item{Type: nanoTypes[q.Type], State: state, Question: InstructionsText(q.Instructions), Options: texts})
	return rawAnswer(q.Type, opts, softmax(scored.Scores)), scored.Tokens, nil
}

// rawAnswer reads probabilities over opts the way the Python runtime reports them.
func rawAnswer(qtype string, opts []Option, probs []float64) RawAnswer {
	a := RawAnswer{Type: qtype, Probabilities: make(map[string]float64, len(opts))}
	best := 0
	for i, o := range opts {
		a.Probabilities[o.Key] = probs[i]
		if probs[i] > probs[best] {
			best = i
		}
	}
	switch qtype {
	case TypeChoice:
		a.Choice = opts[best].Key
	case TypeNoul:
		a.Noul = &probs[0] // options are true, then false
	}
	return a
}

// softmax turns Nano's option scores into probabilities. The C program only
// keeps the highest score; the softmax keeps its order and adds how sure it is.
func softmax(scores []float32) []float64 {
	out := make([]float64, len(scores))
	if len(scores) == 0 {
		return out
	}
	peak := math.Inf(-1)
	for _, s := range scores {
		peak = math.Max(peak, float64(s))
	}
	sum := 0.0
	for i, s := range scores {
		out[i] = math.Exp(float64(s) - peak)
		sum += out[i]
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}

func (*nanoSession) device() string { return "cpu" }

func (*nanoSession) alive() bool { return true }

func (*nanoSession) stop(context.Context) {}
