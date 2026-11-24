package nano

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// WAV format codes.
const (
	wavPCM        = 1
	wavFloat      = 3
	wavExtensible = 0xFFFE
)

// resampleTaps is the half-width of the windowed-sinc resampler, in input
// samples at the lower of the two rates.
const resampleTaps = 16

// PrepareSpeech decodes a WAV file into the 16 kHz mono samples the speech
// model reads: channels are averaged, the rate converted and the clip cut at
// MaxSpeechSeconds.
func PrepareSpeech(data []byte) ([]int16, error) {
	samples, rate, err := decodeWAV(data)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, errors.New("the audio clip is empty")
	}
	if limit := MaxSpeechSeconds * rate; len(samples) > limit {
		samples = samples[:limit]
	}
	return toPCM16(resample(samples, rate, SpeechRate)), nil
}

type wavFormat struct {
	code, channels, rate, bits int
}

// decodeWAV reads a RIFF/WAVE file of integer or float PCM and averages its
// channels into one.
func decodeWAV(data []byte) ([]float32, int, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, errors.New("the audio must be a WAV file")
	}
	var f wavFormat
	var pcm []byte
	for off := 12; off+8 <= len(data); {
		id, size := string(data[off:off+4]), int(binary.LittleEndian.Uint32(data[off+4:]))
		body := data[off+8 : min(off+8+size, len(data))]
		switch id {
		case "fmt ":
			f = readWAVFormat(body)
		case "data":
			pcm = body
		}
		off += 8 + size + size%2 // chunks are padded to even sizes
	}
	if f.channels == 0 || f.rate == 0 || pcm == nil {
		return nil, 0, errors.New("the WAV file has no format or no audio data")
	}
	samples, err := f.decode(pcm)
	return samples, f.rate, err
}

func readWAVFormat(b []byte) wavFormat {
	if len(b) < 16 {
		return wavFormat{}
	}
	f := wavFormat{
		code:     int(binary.LittleEndian.Uint16(b[0:])),
		channels: int(binary.LittleEndian.Uint16(b[2:])),
		rate:     int(binary.LittleEndian.Uint32(b[4:])),
		bits:     int(binary.LittleEndian.Uint16(b[14:])),
	}
	if f.code == wavExtensible && len(b) >= 26 {
		f.code = int(binary.LittleEndian.Uint16(b[24:])) // the sub-format GUID starts with the real code
	}
	return f
}

func (f wavFormat) decode(pcm []byte) ([]float32, error) {
	width := f.bits / 8
	read, err := f.sampleReader()
	if err != nil {
		return nil, err
	}
	frames := len(pcm) / (width * f.channels)
	out := make([]float32, frames)
	for i := range out {
		var sum float32
		for c := range f.channels {
			sum += read(pcm[(i*f.channels+c)*width:])
		}
		out[i] = sum / float32(f.channels)
	}
	return out, nil
}

// sampleReader turns one stored sample into a value in [-1, 1].
func (f wavFormat) sampleReader() (func([]byte) float32, error) {
	switch {
	case f.code == wavPCM && f.bits == 8:
		return func(b []byte) float32 { return (float32(b[0]) - 128) / 128 }, nil
	case f.code == wavPCM && f.bits == 16:
		return func(b []byte) float32 { return float32(int16(binary.LittleEndian.Uint16(b))) / 32768 }, nil
	case f.code == wavPCM && f.bits == 24:
		return func(b []byte) float32 {
			return float32(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)>>8) / 8388608
		}, nil
	case f.code == wavPCM && f.bits == 32:
		return func(b []byte) float32 { return float32(int32(binary.LittleEndian.Uint32(b))) / 2147483648 }, nil
	case f.code == wavFloat && f.bits == 32:
		return func(b []byte) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b)) }, nil
	case f.code == wavFloat && f.bits == 64:
		return func(b []byte) float32 { return float32(math.Float64frombits(binary.LittleEndian.Uint64(b))) }, nil
	}
	return nil, fmt.Errorf("unsupported WAV encoding (format %d, %d bits); use 16-bit PCM", f.code, f.bits)
}

// resample converts the rate with a Hann-windowed sinc whose cutoff is the
// lower Nyquist frequency, so shrinking the rate does not alias.
func resample(x []float32, from, to int) []float32 {
	if from == to {
		return x
	}
	ratio := float64(to) / float64(from)
	cutoff := math.Min(1, ratio)
	half := float64(resampleTaps) / cutoff
	out := make([]float32, int(float64(len(x))*ratio))
	for i := range out {
		centre := float64(i) / ratio
		lo, hi := int(math.Ceil(centre-half)), int(math.Floor(centre+half))
		var acc, norm float64
		for j := max(lo, 0); j <= min(hi, len(x)-1); j++ {
			d := float64(j) - centre
			w := cutoff * sinc(cutoff*d) * (0.5 + 0.5*math.Cos(math.Pi*d/half))
			acc += w * float64(x[j])
			norm += w
		}
		if norm != 0 {
			out[i] = float32(acc / norm)
		}
	}
	return out
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}

func toPCM16(x []float32) []int16 {
	out := make([]int16, len(x))
	for i, v := range x {
		out[i] = int16(math.Round(math.Max(-1, math.Min(1, float64(v))) * 32767))
	}
	return out
}
