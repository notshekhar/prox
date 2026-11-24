package nano

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
)

// Speech front end: 16 kHz audio, 25 ms windows every 10 ms, 80 mel bands.
const (
	SpeechRate  = 16000
	nFFT        = 400
	hop         = 160
	nBins       = nFFT/2 + 1
	nMel        = 80
	frameLen    = 320 // samples per encoder position after the stride-2 conv
	speechMagic = "JNS1"
)

// MaxSpeechSeconds is how much of a clip the encoder reads; callers cut longer clips.
const MaxSpeechSeconds = 12

// linearQ is a weight matrix stored as int8 rows or int4 groups of 32.
type linearQ struct {
	bits, out, in int
	scale         []float32 // int8: one per output
	scale16       []byte    // int4: one f16 per group of 32
	w             []byte
	bias          []float32
}

type encoderLayer struct {
	ln1w, ln1b, ln2w, ln2b []float32
	q, k, v, o, fc1, fc2   linearQ
}

// SpeechModel is a loaded nano_speech.bin: a small transformer encoder over
// log-mel features that scores the intents in labels/speech.json.
type SpeechModel struct {
	d, heads, ffn, outputs, maxPos int
	window, melFilters             []float32
	conv1, conv2, head1, head2     linearQ
	layers                         []encoderLayer
	lnw, lnb, query                []float32
}

// LoadSpeechModel reads and checks a nano_speech.bin.
func LoadSpeechModel(path string) (*SpeechModel, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseSpeechModel(blob)
}

// ParseSpeechModel reads the JNS1 layout written for nano_speech.c.
func ParseSpeechModel(blob []byte) (*SpeechModel, error) {
	r := &reader{b: blob}
	if !bytes.Equal(r.bytes(4), []byte(speechMagic)) {
		return nil, errors.New("not a Julia 2 Nano speech model")
	}
	m := &SpeechModel{d: int(r.u32())}
	nLayers := int(r.u32())
	m.heads, m.ffn, m.outputs, m.maxPos = int(r.u32()), int(r.u32()), int(r.u32()), int(r.u32())
	if m.heads <= 0 || m.d%m.heads != 0 || nLayers > 64 {
		return nil, errors.New("unexpected speech model shape")
	}
	m.window = r.f32s(nFFT)
	m.melFilters = r.f32s(nBins * nMel)
	m.conv1, m.conv2 = readLinearQ(r), readLinearQ(r)
	m.layers = make([]encoderLayer, nLayers)
	for i := range m.layers {
		m.layers[i] = m.readLayer(r)
	}
	m.lnw, m.lnb, m.query = r.f32s(m.d), r.f32s(m.d), r.f32s(m.d)
	m.head1, m.head2 = readLinearQ(r), readLinearQ(r)
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(blob) {
		return nil, fmt.Errorf("size mismatch: read %d of %d bytes", r.off, len(blob))
	}
	return m, nil
}

func (m *SpeechModel) readLayer(r *reader) encoderLayer {
	var l encoderLayer
	l.ln1w, l.ln1b = r.f32s(m.d), r.f32s(m.d)
	l.q, l.k, l.v, l.o = readLinearQ(r), readLinearQ(r), readLinearQ(r), readLinearQ(r)
	l.ln2w, l.ln2b = r.f32s(m.d), r.f32s(m.d)
	l.fc1, l.fc2 = readLinearQ(r), readLinearQ(r)
	return l
}

func readLinearQ(r *reader) linearQ {
	l := linearQ{bits: int(r.u32()), out: int(r.u32()), in: int(r.u32())}
	if l.bits == 8 {
		l.scale = r.f32s(l.out)
		l.w = r.bytes(l.out * l.in)
	} else {
		l.scale16 = r.bytes(l.out * (l.in / 32) * 2)
		l.w = r.bytes(l.out * l.in / 2)
	}
	l.bias = r.f32s(l.out)
	return l
}

func (l *linearQ) apply(x, y []float32) {
	for o := range l.out {
		var acc float32
		if l.bits == 8 {
			w := l.w[o*l.in : (o+1)*l.in]
			for i, b := range w {
				acc += float32(int8(b)) * x[i]
			}
			acc *= l.scale[o]
		} else {
			acc = l.int4Row(o, x)
		}
		y[o] = acc + l.bias[o]
	}
}

func (l *linearQ) int4Row(o int, x []float32) float32 {
	w := l.w[o*l.in/2:]
	s := l.scale16[o*(l.in/32)*2:]
	var acc float32
	for g := range l.in / 32 {
		var part float32
		for j := range 16 {
			b := w[g*16+j]
			part += float32(signed4(int(b&0xF)))*x[g*32+2*j] + float32(signed4(int(b>>4)))*x[g*32+2*j+1]
		}
		acc += part * f16(s[2*g], s[2*g+1])
	}
	return acc
}

// Logits scores one clip of 16 kHz mono samples, at most MaxSpeechSeconds long.
func (m *SpeechModel) Logits(pcm []int16) ([]float32, error) {
	if len(pcm) == 0 {
		return nil, errors.New("the audio clip is empty")
	}
	mel, frames, stride := m.logMel(pcm)
	c1, t1 := m.conv(&m.conv1, mel, nMel, frames, stride, 1)
	x, t2 := m.conv(&m.conv2, transpose(c1, t1, m.d), m.d, t1, t1, 2)
	if t2 > m.maxPos {
		return nil, fmt.Errorf("the audio clip is longer than %d s", MaxSpeechSeconds)
	}
	m.addPositions(x, t2)
	for i := range m.layers {
		m.encoderLayer(&m.layers[i], x, t2)
	}
	return m.head(x, t2, len(pcm)), nil
}

// logMel is the Whisper-style log-mel spectrogram the reference computes:
// reflect padding, a plain DFT per frame, log10 clipped 8 below the peak.
func (m *SpeechModel) logMel(pcm []int16) ([]float32, int, int) {
	n := len(pcm)
	length := n + 2*frameLen
	frames := 1 + length/hop
	wav := make([]float64, length+nFFT)
	for i, s := range pcm {
		wav[i+nFFT/2] = float64(s) / 32767.0
	}
	for i := range nFFT / 2 {
		wav[nFFT/2-1-i] = wav[nFFT/2+1+i]
		wav[nFFT/2+length+i] = wav[nFFT/2+length-2-i]
	}
	keep := frames - 1
	mel := make([]float32, nMel*keep)
	top := -1e30
	frame, power := make([]float64, nFFT), make([]float64, nBins)
	for t := range keep {
		for i := range nFFT {
			frame[i] = wav[t*hop+i] * float64(m.window[i])
		}
		dftPower(frame, power)
		for b := range nMel {
			var s float64
			for k := range nBins {
				s += float64(m.melFilters[k*nMel+b]) * power[k]
			}
			v := math.Log10(math.Max(s, 1e-10))
			mel[b*keep+t] = float32(v)
			top = math.Max(top, v)
		}
	}
	for i, v := range mel {
		if float64(v) < top-8.0 { // compared in double, as the reference does
			v = float32(top - 8.0)
		}
		mel[i] = (v + 4) / 4
	}
	return mel, keep - keep%2, length / hop
}

var dftCos, dftSin = dftTables()

func dftTables() ([nFFT]float64, [nFFT]float64) {
	var c, s [nFFT]float64
	for k := range nFFT {
		c[k] = math.Cos(2 * math.Pi * float64(k) / nFFT)
		s[k] = math.Sin(2 * math.Pi * float64(k) / nFFT)
	}
	return c, s
}

func dftPower(frame, power []float64) {
	for k := range nBins {
		var re, im float64
		for i, v := range frame {
			j := (k * i) % nFFT
			re += v * dftCos[j]
			im -= v * dftSin[j]
		}
		power[k] = re*re + im*im
	}
}

// conv is a kernel-3 convolution over time with GELU; in is channel-major
// with stride values per channel. Output is time-major.
func (m *SpeechModel) conv(l *linearQ, in []float32, cin, t, stride, step int) ([]float32, int) {
	t2 := (t+2-3)/step + 1
	col := make([]float32, cin*3)
	out := make([]float32, t2*l.out)
	for ti := range t2 {
		for c := range cin {
			for k := range 3 {
				x := ti*step + k - 1
				col[c*3+k] = 0
				if x >= 0 && x < t {
					col[c*3+k] = in[c*stride+x]
				}
			}
		}
		row := out[ti*l.out : (ti+1)*l.out]
		l.apply(col, row)
		for i, v := range row {
			row[i] = gelu(v)
		}
	}
	return out, t2
}

func transpose(x []float32, t, d int) []float32 {
	out := make([]float32, len(x))
	for ti := range t {
		for di := range d {
			out[di*t+ti] = x[ti*d+di]
		}
	}
	return out
}

// addPositions adds sinusoidal positions: sines in the first half, cosines in the second.
func (m *SpeechModel) addPositions(x []float32, t int) {
	half := m.d / 2
	inc := math.Log(10000.0) / float64(half-1)
	for ti := range t {
		for i := range half {
			a := float64(ti) * math.Exp(-inc*float64(i))
			x[ti*m.d+i] += float32(math.Sin(a))
			x[ti*m.d+half+i] += float32(math.Cos(a))
		}
	}
}

func layerNorm(x, y, w, b []float32) {
	n := float64(len(x))
	var mean, variance float64
	for _, v := range x {
		mean += float64(v)
	}
	mean /= n
	for _, v := range x {
		variance += (float64(v) - mean) * (float64(v) - mean)
	}
	variance /= n
	r := float32(1 / math.Sqrt(variance+1e-5))
	for i, v := range x {
		y[i] = float32(float64(v)-mean)*r*w[i] + b[i]
	}
}

func (m *SpeechModel) encoderLayer(l *encoderLayer, x []float32, t int) {
	d := m.d
	h, k, v := make([]float32, t*d), make([]float32, t*d), make([]float32, t*d)
	for ti := range t {
		layerNorm(x[ti*d:(ti+1)*d], h[ti*d:(ti+1)*d], l.ln1w, l.ln1b)
		l.k.apply(h[ti*d:(ti+1)*d], k[ti*d:(ti+1)*d])
		l.v.apply(h[ti*d:(ti+1)*d], v[ti*d:(ti+1)*d])
	}
	upd := make([]float32, t*d)
	q, att := make([]float32, d), make([]float32, d)
	scores := make([]float32, t)
	for ti := range t {
		l.q.apply(h[ti*d:(ti+1)*d], q)
		m.attend(q, k, v, t, scores, att)
		l.o.apply(att, upd[ti*d:(ti+1)*d])
	}
	for i := range x {
		x[i] += upd[i]
	}
	m.feedForward(l, x, t)
}

// attend is multi-head attention of one query over every position.
func (m *SpeechModel) attend(q, k, v []float32, t int, scores, att []float32) {
	d, hd := m.d, m.d/m.heads
	scaling := 1 / float32(math.Sqrt(float64(hd)))
	for head := range m.heads {
		peak := float32(math.Inf(-1))
		for s := range t {
			var a float32
			for j := range hd {
				a += q[head*hd+j] * k[s*d+head*hd+j]
			}
			scores[s] = a * scaling
			peak = max(peak, scores[s])
		}
		var z float64
		for s := range t {
			scores[s] = float32(math.Exp(float64(scores[s] - peak)))
			z += float64(scores[s])
		}
		for j := range hd {
			var a float64
			for s := range t {
				a += float64(scores[s] * v[s*d+head*hd+j])
			}
			att[head*hd+j] = float32(a / z)
		}
	}
}

func (m *SpeechModel) feedForward(l *encoderLayer, x []float32, t int) {
	d := m.d
	o, f1, f2 := make([]float32, d), make([]float32, m.ffn), make([]float32, d)
	for ti := range t {
		row := x[ti*d : (ti+1)*d]
		layerNorm(row, o, l.ln2w, l.ln2b)
		l.fc1.apply(o, f1)
		for i, v := range f1 {
			f1[i] = gelu(v)
		}
		l.fc2.apply(f1, f2)
		for i := range row {
			row[i] += f2[i]
		}
	}
}

// head pools the positions that hold real audio with a learned query, then
// scores each intent.
func (m *SpeechModel) head(x []float32, t, samples int) []float32 {
	d := m.d
	n := min(max((samples+frameLen-1)/frameLen, 1), t)
	h := make([]float32, t*d)
	scores := make([]float64, n)
	peak := -1e30
	for ti := range t {
		layerNorm(x[ti*d:(ti+1)*d], h[ti*d:(ti+1)*d], m.lnw, m.lnb)
		if ti < n {
			var a float64
			for j := range d {
				a += float64(h[ti*d+j] * m.query[j])
			}
			scores[ti] = float64(float32(a))
			peak = math.Max(peak, a)
		}
	}
	att := make([]float32, d)
	var z float64
	for ti := range n {
		e := math.Exp(scores[ti] - peak)
		z += e
		scores[ti] = float64(float32(e))
	}
	for ti := range n {
		w := float32(scores[ti] / z)
		for j := range d {
			att[j] += w * h[ti*d+j]
		}
	}
	hid := make([]float32, m.head1.out)
	m.head1.apply(att, hid)
	for i, v := range hid {
		hid[i] = gelu(v)
	}
	logits := make([]float32, m.outputs)
	m.head2.apply(hid, logits)
	return logits
}
