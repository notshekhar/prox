package localmodels

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels/nano"
)

// Files of the Julia 2 Nano repository the media models need.
const (
	nanoImageWeights  = "image/nano_image.bin"
	nanoSpeechWeights = "speech/nano_speech.bin"
	nanoImageLabels   = "labels/image.json"
	nanoSpeechLabels  = "labels/speech.json"
)

// labelSet is a closed list of answers a media model scores.
type labelSet struct {
	names []string
	index map[string]int // normalised name → position
}

func loadLabels(path string) (labelSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return labelSet{}, err
	}
	var names []string
	if err = json.Unmarshal(raw, &names); err != nil {
		return labelSet{}, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	set := labelSet{names: names, index: make(map[string]int, len(names))}
	for i, n := range names {
		set.index[normaliseLabel(n)] = i
	}
	return set, nil
}

// normaliseLabel lets "Golf_Ball", "golf-ball" and "golf ball" name one label.
func normaliseLabel(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == ' ' || r == '_' || r == '-'
	}), " ")
}

// find matches an option to a label by its key, then by its description.
func (l labelSet) find(o Option) (int, bool) {
	for _, s := range []string{o.Key, o.Description} {
		if i, ok := l.index[normaliseLabel(s)]; ok && s != "" {
			return i, true
		}
	}
	return 0, false
}

// nanoMedia holds Nano's image and speech models.
type nanoMedia struct {
	image        *nano.ImageModel
	speech       *nano.SpeechModel
	imageLabels  labelSet
	speechLabels labelSet
}

func loadNanoMedia(dir string) (nanoMedia, error) {
	var m nanoMedia
	var err error
	path := func(rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
	if m.image, err = nano.LoadImageModel(path(nanoImageWeights)); err != nil {
		return m, err
	}
	if m.speech, err = nano.LoadSpeechModel(path(nanoSpeechWeights)); err != nil {
		return m, err
	}
	if m.imageLabels, err = loadLabels(path(nanoImageLabels)); err != nil {
		return m, err
	}
	m.speechLabels, err = loadLabels(path(nanoSpeechLabels))
	return m, err
}

// decide answers choice questions about the one image or clip a request
// carries. Nano's media models score closed label sets and read no text.
func (m nanoMedia) decide(req Request) (Result, error) {
	asset, err := soleMedia(req)
	if err != nil {
		return Result{}, err
	}
	logits, labels, err := m.score(asset)
	if err != nil {
		return Result{}, err
	}
	res := Result{Answers: make(map[string]RawAnswer, len(req.Questions))}
	for _, q := range req.Questions {
		a, err := labelAnswer(q, logits, labels, asset.Kind)
		if err != nil {
			return Result{}, err
		}
		res.Answers[q.Name] = a
	}
	return res, nil
}

// soleMedia checks the request is one asset given as the whole state.
func soleMedia(req Request) (Asset, error) {
	if len(req.Assets) != 1 {
		return Asset{}, errors.New("Julia 2 Nano reads one image or one audio clip per request")
	}
	refs := FindReferences(req)
	id := onlyKey(req.Assets)
	if len(refs.StateWhole) == 0 || len(refs.Elsewhere) > 0 {
		return Asset{}, errors.New(`Julia 2 Nano reads an image or clip only as a whole state value, like "state": "<asset id>"`)
	}
	other := false
	eachString(req.State, func(s string) { other = other || (s != id && strings.TrimSpace(s) != "") })
	if other {
		return Asset{}, errors.New("Julia 2 Nano cannot combine an image or clip with text; send it on its own, or use julia-2-flash")
	}
	return req.Assets[id], nil
}

func onlyKey(assets map[string]Asset) string {
	for id := range assets {
		return id
	}
	return ""
}

func (m nanoMedia) score(a Asset) ([]float32, labelSet, error) {
	switch a.Kind {
	case MediaImage:
		rgb, err := nano.PrepareImage(a.Data)
		if err != nil {
			return nil, labelSet{}, err
		}
		logits, err := m.image.Logits(rgb)
		return logits, m.imageLabels, err
	case MediaAudio:
		if a.Format != "wav" {
			return nil, labelSet{}, errors.New("Julia 2 Nano reads WAV audio; use julia-2-flash for FLAC or OGG")
		}
		pcm, err := nano.PrepareSpeech(a.Data)
		if err != nil {
			return nil, labelSet{}, err
		}
		logits, err := m.speech.Logits(pcm)
		return logits, m.speechLabels, err
	}
	return nil, labelSet{}, fmt.Errorf("unsupported asset type %q", a.Kind)
}

// labelAnswer reads a choice question's options as labels and spreads
// probability over just those labels.
func labelAnswer(q Question, logits []float32, labels labelSet, kind MediaKind) (RawAnswer, error) {
	if q.Type != TypeChoice {
		return RawAnswer{}, fmt.Errorf("question %q: Julia 2 Nano answers %s with choice questions whose options are its labels", q.Name, kind)
	}
	opts, err := Options(q)
	if err != nil {
		return RawAnswer{}, err
	}
	scores := make([]float32, len(opts))
	for i, o := range opts {
		at, ok := labels.find(o)
		if !ok {
			return RawAnswer{}, fmt.Errorf("question %q: option %q is not one of Julia 2 Nano's %s labels (%s)", q.Name, o.Key, kind, labels.hint())
		}
		scores[i] = logits[at]
	}
	return rawAnswer(TypeChoice, opts, softmax(scores)), nil
}

// hint lists the labels, or a sample when there are many.
func (l labelSet) hint() string {
	const shown = 14
	if len(l.names) <= shown {
		return strings.Join(l.names, ", ")
	}
	return strings.Join(l.names[:shown], ", ") + fmt.Sprintf(", … %d in all; GET /v1/systemone/models lists them", len(l.names))
}

// nanoLabels reads the label lists of an installed Nano, for the models list.
func nanoLabels(dir string) map[MediaKind][]string {
	out := map[MediaKind][]string{}
	for kind, rel := range map[MediaKind]string{MediaImage: nanoImageLabels, MediaAudio: nanoSpeechLabels} {
		if set, err := loadLabels(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			out[kind] = set.names
		}
	}
	return out
}
