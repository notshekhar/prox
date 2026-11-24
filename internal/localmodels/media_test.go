package localmodels

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strings"
	"testing"
)

const photoID = "6e1d0b7c-photo"

func photo() Asset { return Asset{Kind: MediaImage, Format: "png", Data: []byte("png bytes")} }

func TestFindReferences(t *testing.T) {
	req := Request{
		State: jsontext.Value(`{"photo":"` + photoID + `","note":"see clip-9 later","6e1d0b7c-photo":"key, not a reference"}`),
		Questions: []Question{
			{Name: "q", Type: TypeChoice, Criteria: jsontext.Value(`{"a":"looks like ref-c"}`)},
		},
		Assets: map[string]Asset{photoID: photo(), "clip-9": {Kind: MediaAudio}, "ref-c": photo(), "unused": photo()},
	}
	refs := FindReferences(req)
	if !slices.Equal(refs.StateWhole, []string{photoID}) {
		t.Errorf("whole = %v", refs.StateWhole)
	}
	if !refs.Elsewhere["clip-9"] || !refs.Elsewhere["ref-c"] || refs.Elsewhere[photoID] {
		t.Errorf("elsewhere = %v", refs.Elsewhere)
	}
	if refs.Used["unused"] {
		t.Error("an asset only named as an object key counted as used")
	}
}

func TestReplaceWholeReferencesKeepsOrder(t *testing.T) {
	assets := map[string]Asset{"b-img": photo(), "a-clip": {Kind: MediaAudio}, "c-img": photo()}
	got, err := ReplaceWholeReferences(jsontext.Value(`{"z":"b-img","y":["a-clip","c-img","b-img"],"x":"text"}`), assets)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"z":"[image 1]","y":["[audio 1]","[image 2]","[image 1]"],"x":"text"}`
	if string(got) != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestJuliaStatePutsMediaFirst(t *testing.T) {
	clip := Asset{Kind: MediaAudio, Format: "wav", Data: []byte("RIFF")}
	state, err := juliaState(Request{
		State:  jsontext.Value(`{"call":"clip-1","photo":"` + photoID + `"}`),
		Assets: map[string]Asset{photoID: photo(), "clip-1": clip},
	})
	if err != nil {
		t.Fatal(err)
	}
	var parts []map[string]string
	if err = json.Unmarshal(state, &parts); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 || parts[0]["type"] != "input_audio" || parts[1]["type"] != "image" || parts[2]["type"] != "text" {
		t.Fatalf("parts = %v", parts)
	}
	if !strings.HasPrefix(parts[0]["input_audio"], "data:audio/wav;base64,") || !strings.HasPrefix(parts[1]["image"], "data:image/png;base64,") {
		t.Fatalf("media must reach the runtime as data URLs, never paths: %v", parts[:2])
	}
	if parts[2]["text"] != `{"call":"[audio 1]","photo":"[image 1]"}` {
		t.Fatalf("text = %s", parts[2]["text"])
	}
}

func TestJuliaStateRefusesMediaOutsideTheState(t *testing.T) {
	_, err := juliaState(Request{
		State:     jsontext.Value(`"no media here"`),
		Questions: []Question{{Name: "q", Type: TypeNoul, Instructions: jsontext.Value(`"Is ` + photoID + ` blurry?"`)}},
		Assets:    map[string]Asset{photoID: photo()},
	})
	if err == nil || !strings.Contains(err.Error(), "whole values in the state") {
		t.Fatalf("err = %v", err)
	}
}

func TestLabelMatching(t *testing.T) {
	set := labelSet{names: []string{"golf ball", "English springer"}, index: map[string]int{"golf ball": 0, "english springer": 1}}
	for _, o := range []Option{{Key: "Golf_Ball"}, {Key: "x", Description: "golf-ball"}, {Key: "english springer"}} {
		if _, ok := set.find(o); !ok {
			t.Errorf("%+v did not match", o)
		}
	}
	if _, ok := set.find(Option{Key: "golf"}); ok {
		t.Error("a partial name matched")
	}
}

// pngOf is a flat-colour PNG, the smallest real image the decoder accepts.
func pngOf(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 160, 160))
	for i := 0; i < len(img.Pix); i += 4 {
		r, g, b, _ := c.RGBA()
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(r>>8), byte(g>>8), byte(b>>8), 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// wavOf is one second of a 440 Hz-ish square wave at 16 kHz.
func wavOf() []byte {
	var pcm bytes.Buffer
	for i := range 16000 {
		v := int16(4000)
		if (i/18)%2 == 0 {
			v = -4000
		}
		_ = binary.Write(&pcm, binary.LittleEndian, v)
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+pcm.Len()))
	b.WriteString("WAVEfmt ")
	for _, f := range []any{uint32(16), uint16(1), uint16(1), uint32(16000), uint32(32000), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, f)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(pcm.Len()))
	b.Write(pcm.Bytes())
	return b.Bytes()
}

func mediaRequest(asset Asset, state string, questions ...Question) Request {
	return Request{State: jsontext.Value(state), Questions: questions, Assets: map[string]Asset{"m-1": asset}}
}

func choice(criteria string) Question {
	return Question{Name: "what", Type: TypeChoice, Criteria: jsontext.Value(criteria)}
}

func TestNanoAnswersAboutAnImageAndAClip(t *testing.T) {
	mg, m := realNanoManager(t)
	img := Asset{Kind: MediaImage, Format: "png", Data: pngOf(t, color.NRGBA{250, 210, 20, 255})}
	res, err := mg.Decide(context.Background(), m, mediaRequest(img, `"m-1"`, choice(`{"sunflower":"","golf_ball":"","church":""}`)))
	if err != nil {
		t.Fatal(err)
	}
	assertDistribution(t, res.Answers["what"].Probabilities, 3)
	clip := Asset{Kind: MediaAudio, Format: "wav", Data: wavOf()}
	res, err = mg.Decide(context.Background(), m, mediaRequest(clip, `{"call":"m-1"}`, choice(`{"balance":"","freeze":"Block my card"}`)))
	if err != nil {
		t.Fatal(err)
	}
	assertDistribution(t, res.Answers["what"].Probabilities, 2)
}

func TestNanoMediaRules(t *testing.T) {
	mg, m := realNanoManager(t)
	img := Asset{Kind: MediaImage, Format: "png", Data: pngOf(t, color.White)}
	cases := map[string]struct {
		req  Request
		want string
	}{
		"unknown label": {mediaRequest(img, `"m-1"`, choice(`{"teapot":""}`)), `"teapot" is not one of`},
		"yes/no":        {mediaRequest(img, `"m-1"`, Question{Name: "q", Type: TypeNoul}), "choice questions"},
		"with text":     {mediaRequest(img, `{"photo":"m-1","note":"a sunny day"}`, choice(`{"sunflower":""}`)), "cannot combine"},
		"flac":          {mediaRequest(Asset{Kind: MediaAudio, Format: "flac", Data: []byte("fLaC")}, `"m-1"`, choice(`{"balance":""}`)), "WAV"},
	}
	for name, tc := range cases {
		_, err := mg.Decide(context.Background(), m, tc.req)
		var reqErr *RequestError
		if !errors.As(err, &reqErr) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a RequestError mentioning %q", name, err, tc.want)
		}
	}
}
