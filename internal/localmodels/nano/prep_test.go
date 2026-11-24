package nano

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
)

// A 145-pixel side cropped to 128 must start at 8, as torchvision's
// half-to-even round gives, not 9; one pixel off shifts every row.
func TestCentreCropRoundsHalfToEven(t *testing.T) {
	img := rgbImage{w: 145, h: 145, pix: make([]byte, 145*145*3)}
	for y := range 145 {
		for x := range 145 {
			img.pix[(y*145+x)*3] = byte(x)
			img.pix[(y*145+x)*3+1] = byte(y)
		}
	}
	crop := centreCrop(img, 128)
	if x, y := crop[0], crop[1]; x != 8 || y != 8 {
		t.Fatalf("crop starts at (%d, %d), want (8, 8)", x, y)
	}
}

func encodePNG(t *testing.T, w, h int, fill color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPrepareImageKeepsAFlatColour(t *testing.T) {
	rgb, err := PrepareImage(encodePNG(t, 400, 300, color.NRGBA{200, 100, 50, 255}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rgb) != ImageSide*ImageSide*3 {
		t.Fatalf("got %d bytes", len(rgb))
	}
	for i := 0; i < len(rgb); i += 3 {
		if rgb[i] != 200 || rgb[i+1] != 100 || rgb[i+2] != 50 {
			t.Fatalf("pixel %d = %v, want the flat colour", i/3, rgb[i:i+3])
		}
	}
}

// Transparent pixels keep their colour, as Pillow's convert("RGB") does.
func TestPrepareImageDropsAlphaWithoutBlending(t *testing.T) {
	rgb, err := PrepareImage(encodePNG(t, 200, 200, color.NRGBA{10, 220, 30, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if rgb[0] != 10 || rgb[1] != 220 || rgb[2] != 30 {
		t.Fatalf("pixel = %v", rgb[:3])
	}
}

func TestPrepareImageRejects(t *testing.T) {
	if _, err := PrepareImage([]byte("not an image")); err == nil {
		t.Error("accepted text as an image")
	}
	// A PNG header claiming 50 000 × 50 000 must be refused before decoding.
	ihdr := []byte("IHDR")
	ihdr = binary.BigEndian.AppendUint32(ihdr, 50000)
	ihdr = binary.BigEndian.AppendUint32(ihdr, 50000)
	ihdr = append(ihdr, 8, 2, 0, 0, 0)
	hdr := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d"), ihdr...)
	hdr = binary.BigEndian.AppendUint32(hdr, crc32.ChecksumIEEE(ihdr))
	if _, err := PrepareImage(hdr); err == nil || !strings.Contains(err.Error(), "pixels") {
		t.Errorf("pixel bomb: err = %v", err)
	}
}

// wavFile builds a WAV with one fmt chunk and one data chunk.
func wavFile(code, channels, rate, bits int, data []byte) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+len(data)))
	b.WriteString("WAVEfmt ")
	for _, field := range []any{uint32(16), uint16(code), uint16(channels), uint32(rate),
		uint32(rate * channels * bits / 8), uint16(channels * bits / 8), uint16(bits)} {
		_ = binary.Write(&b, binary.LittleEndian, field)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

func TestDecodeWAVFormats(t *testing.T) {
	half := func(bits int) []byte { // one stereo frame at +0.5 in both channels
		var b bytes.Buffer
		switch bits {
		case 16:
			_ = binary.Write(&b, binary.LittleEndian, []int16{16384, 16384})
		case 24:
			b.Write([]byte{0, 0, 0x40, 0, 0, 0x40})
		case 32:
			_ = binary.Write(&b, binary.LittleEndian, []int32{1 << 30, 1 << 30})
		}
		return b.Bytes()
	}
	var f32 bytes.Buffer
	_ = binary.Write(&f32, binary.LittleEndian, []float32{0.5, 0.5})
	cases := map[string][]byte{
		"pcm16":   wavFile(wavPCM, 2, 16000, 16, half(16)),
		"pcm24":   wavFile(wavPCM, 2, 16000, 24, half(24)),
		"pcm32":   wavFile(wavPCM, 2, 16000, 32, half(32)),
		"float32": wavFile(wavFloat, 2, 16000, 32, f32.Bytes()),
		"pcm8":    wavFile(wavPCM, 2, 16000, 8, []byte{192, 192}),
	}
	for name, file := range cases {
		samples, rate, err := decodeWAV(file)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if rate != 16000 || len(samples) != 1 || math.Abs(float64(samples[0])-0.5) > 1e-3 {
			t.Errorf("%s: rate %d samples %v, want one 0.5 at 16000", name, rate, samples)
		}
	}
}

func TestDecodeWAVRejects(t *testing.T) {
	for name, file := range map[string][]byte{
		"not wav": []byte("ID3 an mp3 file"),
		"no data": wavFile(wavPCM, 1, 16000, 16, nil)[:36],
		"alaw":    wavFile(6, 1, 8000, 8, []byte{1, 2}),
		"empty":   nil,
	} {
		if _, err := PrepareSpeech(file); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPrepareSpeechResamplesAndCuts(t *testing.T) {
	// 20 s of a constant at 44.1 kHz: cut to 12 s, converted to 16 kHz, level kept.
	n := 20 * 44100
	data := make([]int16, n)
	for i := range data {
		data[i] = 8192
	}
	var pcm bytes.Buffer
	_ = binary.Write(&pcm, binary.LittleEndian, data)
	out, err := PrepareSpeech(wavFile(wavPCM, 1, 44100, 16, pcm.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if want := MaxSpeechSeconds * SpeechRate; len(out) != want {
		t.Fatalf("got %d samples, want %d", len(out), want)
	}
	if mid := out[len(out)/2]; math.Abs(float64(mid)-8191.75) > 2 {
		t.Fatalf("level changed to %d", mid)
	}
}
