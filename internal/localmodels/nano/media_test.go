package nano

import (
	json "encoding/json/v2"
	"math"
	"os"
	"testing"
)

// mediaTolerance allows float rounding differences against the reference C.
// The image network rounds activations onto a grid before every layer, so a
// last-bit difference can flip one grid step: the reference C itself differs
// by 3.6e-3 between builds with and without fused multiply-adds. The Go port
// measured 8.8e-4 for images and 2.2e-6 for speech.
const mediaTolerance = 5e-3

type mediaReference struct {
	Clips        [][3]int    `json:"clips"` // samples, seed, square-wave period
	ImageLogits  [][]float64 `json:"image_logits"`
	SpeechLogits [][]float64 `json:"speech_logits"`
}

func loadMediaReference(t *testing.T) mediaReference {
	t.Helper()
	raw, err := os.ReadFile("testdata/media_reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref mediaReference
	if err = json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

// syntheticImage and syntheticClip use integer maths only, so the Python that
// fed the reference C produced exactly the same bytes.
func syntheticImage(i int) []byte {
	out := make([]byte, 0, ImageSide*ImageSide*3)
	for y := range ImageSide {
		for x := range ImageSide {
			for ch := range 3 {
				out = append(out, byte((x*(3+i)+y*(5+2*i)+ch*(29+i)+((x*y)%(17+i))*(i+1))%256))
			}
		}
	}
	return out
}

func syntheticClip(n, seed, period int) []int16 {
	x, out := seed, make([]int16, n)
	for k := range out {
		x = (x*1103515245 + 12345) % (1 << 31)
		noise := (x>>16)%20001 - 10000
		square := -6000
		if (k/period)%2 == 1 {
			square = 6000
		}
		out[k] = int16(floorDiv(noise, 4) + square)
	}
	return out
}

// floorDiv matches Python's // on negative numbers.
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func assertLogits(t *testing.T, what string, got []float32, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d logits, reference has %d", what, len(got), len(want))
	}
	worst := 0.0
	for i := range want {
		worst = math.Max(worst, math.Abs(float64(got[i])-want[i]))
	}
	if worst > mediaTolerance {
		t.Errorf("%s: largest logit difference %.3g", what, worst)
	}
	if best, refBest := argmax(got), argmaxF64(want); best != refBest {
		t.Errorf("%s: top label %d, reference %d", what, best, refBest)
	}
	t.Logf("%s: largest logit difference %.3g", what, worst)
}

func argmaxF64(v []float64) int {
	best := 0
	for i, x := range v {
		if x > v[best] {
			best = i
		}
	}
	return best
}

func TestImageLogitsMatchTheReference(t *testing.T) {
	m, err := LoadImageModel(nanoFile(t, "image/nano_image.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range loadMediaReference(t).ImageLogits {
		got, err := m.Logits(syntheticImage(i))
		if err != nil {
			t.Fatal(err)
		}
		assertLogits(t, "image "+string(rune('0'+i)), got, want)
	}
}

func TestSpeechLogitsMatchTheReference(t *testing.T) {
	m, err := LoadSpeechModel(nanoFile(t, "speech/nano_speech.bin"))
	if err != nil {
		t.Fatal(err)
	}
	ref := loadMediaReference(t)
	for i, want := range ref.SpeechLogits {
		c := ref.Clips[i]
		got, err := m.Logits(syntheticClip(c[0], c[1], c[2]))
		if err != nil {
			t.Fatal(err)
		}
		assertLogits(t, "clip "+string(rune('0'+i)), got, want)
	}
}

func TestMediaModelsRejectOtherFiles(t *testing.T) {
	if _, err := ParseImageModel([]byte("JNS1")); err == nil {
		t.Error("image parser took a speech header")
	}
	if _, err := ParseSpeechModel([]byte("JNI1")); err == nil {
		t.Error("speech parser took an image header")
	}
	if _, err := ParseImageModel(append([]byte("JNI1"), 1, 0, 0, 0, 9, 0, 0, 0)); err == nil {
		t.Error("image parser took an unknown op")
	}
}
