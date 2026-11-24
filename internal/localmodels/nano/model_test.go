package nano

import (
	"encoding/base64"
	json "encoding/json/v2"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// dirEnv points at a downloaded Julia-2-Nano repository (text/, image/,
// speech/). The weights are 15 MB and not in this repo, so the golden tests
// only run when it is set.
const dirEnv = "PROX_NANO_DIR"

// nanoFile resolves a file in the downloaded repository, or skips the test.
func nanoFile(t *testing.T, rel string) string {
	t.Helper()
	dir := os.Getenv(dirEnv)
	if dir == "" {
		t.Skipf("set %s to a Julia-2-Nano download to compare against the reference C", dirEnv)
	}
	return filepath.Join(dir, filepath.FromSlash(rel))
}

// scoreTolerance allows for float rounding: C may fuse multiply-adds that Go
// rounds separately, and erff and Erf differ in the last bits. Measured: 1.3e-8.
const scoreTolerance = 1e-6

type referenceItem struct {
	Type        QuestionType `json:"type"`
	StateB64    string       `json:"state_b64"`
	Question    string       `json:"question"`
	Options     []string     `json:"options"`
	StateIDs    []int32      `json:"state_ids"`
	QuestionIDs []int32      `json:"question_ids"`
	OptionIDs   [][]int32    `json:"option_ids"`
	Scores      []float64    `json:"scores"`
	Choice      int          `json:"choice"`
}

func loadReference(t *testing.T) (*Model, []referenceItem) {
	t.Helper()
	m, err := Load(nanoFile(t, "text/nano_text.bin"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Items []referenceItem `json:"items"`
	}
	if err = json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	return m, ref.Items
}

func (it referenceItem) item(t *testing.T) Item {
	state, err := base64.StdEncoding.DecodeString(it.StateB64)
	if err != nil {
		t.Fatal(err)
	}
	return Item{Type: it.Type, State: string(state), Question: it.Question, Options: it.Options}
}

// The reference output covers Portuguese and Latin-1 folding, CJK, emoji,
// malformed UTF-8, an empty state, an over-long word and a text cut at the
// character limit.
func TestTokenIDsMatchTheReference(t *testing.T) {
	m, items := loadReference(t)
	for i, ref := range items {
		it := ref.item(t)
		texts := append([]string{it.State, it.Question}, it.Options...)
		want := append([][]int32{ref.StateIDs, ref.QuestionIDs}, ref.OptionIDs...)
		for j, text := range texts {
			got, _ := m.tokenize(text)
			if !slices.Equal(got, want[j]) && !(len(got) == 0 && len(want[j]) == 0) {
				t.Errorf("item %d text %d (%.40q):\n got %v\nwant %v", i, j, text, got, want[j])
			}
		}
	}
}

func TestScoresMatchTheReference(t *testing.T) {
	m, items := loadReference(t)
	for i, ref := range items {
		got := m.Score(ref.item(t))
		for j, want := range ref.Scores {
			if diff := math.Abs(float64(got.Scores[j]) - want); diff > scoreTolerance {
				t.Errorf("item %d option %d: score %.6f, reference %.6f", i, j, got.Scores[j], want)
			}
		}
		if best := argmax(got.Scores); best != ref.Choice {
			t.Errorf("item %d: chose %d, reference chose %d", i, best, ref.Choice)
		}
	}
}

func TestLongTextIsReportedAsCut(t *testing.T) {
	m, items := loadReference(t)
	if !m.Score(items[7].item(t)).Truncated {
		t.Fatal("a state past the character limit was not reported as cut")
	}
	if m.Score(items[0].item(t)).Truncated {
		t.Fatal("a short item was reported as cut")
	}
}

func argmax(v []float32) int {
	best := 0
	for i, x := range v {
		if x > v[best] {
			best = i
		}
	}
	return best
}

func TestParseRejectsOtherFiles(t *testing.T) {
	for name, blob := range map[string][]byte{
		"wrong magic": []byte("GGUF0000000000000000"),
		"truncated":   append([]byte("JNT3"), 1, 0, 0, 0, 0, 1, 0, 0),
		"empty":       nil,
	} {
		if _, err := Parse(blob); err == nil {
			t.Errorf("%s: parsed without error", name)
		} else if strings.Contains(err.Error(), "panic") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
