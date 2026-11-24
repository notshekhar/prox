package nano

import "math"

// Item is one question about one state, with its options already rendered as
// "key: description" (or the key alone).
type Item struct {
	Type     QuestionType
	State    string
	Question string
	Options  []string
}

// Scored is the network's raw score for each option, in order, plus what the
// texts cost.
type Scored struct {
	Scores    []float32
	Tokens    int
	Truncated bool // some text was longer than the model reads
}

// Score rates every option of an item. The reference picks the highest
// score; callers turn the scores into probabilities.
func (m *Model) Score(it Item) Scored {
	var out Scored
	vs := m.encode(it.State, &out)
	vq := m.encode(it.Question, &out)
	base := m.itemPart(vs, vq, it.Type)
	out.Scores = make([]float32, len(it.Options))
	for i, opt := range it.Options {
		out.Scores[i] = m.optionScore(base, vs, vq, m.encode(opt, &out))
	}
	return out
}

// encode is the text's bag of word pieces: the mean of each table's rows,
// each table's half L2-normalised, then the whole vector.
func (m *Model) encode(text string, acc *Scored) []float32 {
	ids, cut := m.tokenize(text)
	acc.Tokens += len(ids)
	acc.Truncated = acc.Truncated || cut
	out := make([]float32, m.vd)
	for _, id := range ids {
		addInt4(out[:dim], m.tableA[int(id)*int4RowBytes:])
		if m.tables == 2 {
			if k := m.slotB[id]; k&0x8000 != 0 {
				addInt4(out[dim:], m.rows4B[int(k&0x7fff)*int4RowBytes:])
			} else {
				addInt2(out[dim:], m.rows2B[int(k)*int2RowBytes:])
			}
		}
	}
	if len(ids) == 0 {
		return out
	}
	for half := range m.tables {
		part := out[half*dim : (half+1)*dim]
		for i := range part {
			part[i] /= float32(len(ids))
		}
		normalise(part)
	}
	normalise(out)
	return out
}

func normalise(v []float32) {
	var ss float32
	for _, x := range v {
		ss += x * x
	}
	k := 1 / (float32(math.Sqrt(float64(ss))) + 1e-9)
	for i := range v {
		v[i] *= k
	}
}

func addInt4(acc []float32, row []byte) {
	for g := range dim / 32 {
		s := f16(row[0], row[1])
		for j, b := range row[2:18] {
			lo, hi := int(b&0xF), int(b>>4)
			acc[g*32+2*j] += s * float32(signed4(lo))
			acc[g*32+2*j+1] += s * float32(signed4(hi))
		}
		row = row[18:]
	}
}

func signed4(n int) int {
	if n > 7 {
		return n - 16
	}
	return n
}

func addInt2(acc []float32, row []byte) {
	for g := range dim / 32 {
		s := f16(row[0], row[1])
		for j, b := range row[2:10] {
			for k := range 4 {
				acc[g*32+4*j+k] += s * (float32(int(b>>(2*k))&3-2) + 0.5)
			}
		}
		row = row[10:]
	}
}

func f16(lo, hi byte) float32 {
	h := uint32(lo) | uint32(hi)<<8
	sign, exp, man := (h&0x8000)<<16, (h>>10)&0x1f, h&0x3ff
	var f uint32
	switch {
	case exp == 0 && man == 0:
		f = sign
	case exp == 0:
		exp = 127 - 15 + 1
		for man&0x400 == 0 {
			man <<= 1
			exp--
		}
		f = sign | exp<<23 | (man&0x3ff)<<13
	case exp == 31:
		f = sign | 0x7f800000 | man<<13
	default:
		f = sign | (exp+127-15)<<23 | man<<13
	}
	return math.Float32frombits(f)
}

// The first layer's input is eight blocks of vd values plus the question
// type: state, question, option, state·option, question·option, state·question,
// |state−option|, type. Blocks that do not involve the option are computed once
// per item.
const (
	blockState = iota
	blockQuestion
	blockOption
	blockStateOption
	blockQuestionOption
	blockStateQuestion
	blockStateMinusOption
	blockType
)

func (m *Model) itemPart(vs, vq []float32, qtype QuestionType) []int32 {
	d, l := m.vd, &m.layers[0]
	q := make([]int8, 3*d+qtypeDim)
	for i := range d {
		q[i] = q8(vs[i], l.step[blockState*d+i])
		q[d+i] = q8(vq[i], l.step[blockQuestion*d+i])
		q[2*d+i] = q8(vs[i]*vq[i], l.step[blockStateQuestion*d+i])
	}
	emb := m.qtypeEmb[int(qtype)*qtypeDim:]
	for i := range qtypeDim {
		q[3*d+i] = q8(emb[i], l.step[blockType*d+i])
	}
	base := make([]int32, l.out)
	for o := range l.out {
		w := l.w[o*l.in:]
		base[o] = dot8(w[blockState*d:], q[:d]) + dot8(w[blockQuestion*d:], q[d:2*d]) +
			dot8(w[blockStateQuestion*d:], q[2*d:3*d]) + dot8(w[blockType*d:], q[3*d:])
	}
	return base
}

func (m *Model) optionScore(base []int32, vs, vq, vo []float32) float32 {
	d, l := m.vd, &m.layers[0]
	q := make([]int8, 4*d)
	for i := range d {
		q[i] = q8(vo[i], l.step[blockOption*d+i])
		q[d+i] = q8(vs[i]*vo[i], l.step[blockStateOption*d+i])
		q[2*d+i] = q8(vq[i]*vo[i], l.step[blockQuestionOption*d+i])
		q[3*d+i] = q8(float32(math.Abs(float64(vs[i]-vo[i]))), l.step[blockStateMinusOption*d+i])
	}
	h1 := make([]float32, l.out)
	for o := range l.out {
		w := l.w[o*l.in:]
		acc := base[o] + dot8(w[blockOption*d:], q[:d]) + dot8(w[blockStateOption*d:], q[d:2*d]) +
			dot8(w[blockQuestionOption*d:], q[2*d:3*d]) + dot8(w[blockStateMinusOption*d:], q[3*d:])
		h1[o] = gelu(float32(acc)*l.scale[o] + l.bias[o])
	}
	h2 := dense(&m.layers[1], h1, true)
	return dense(&m.layers[2], h2, false)[0]
}

func dense(l *layer, x []float32, act bool) []float32 {
	q := make([]int8, l.in)
	for i := range q {
		q[i] = q8(x[i], l.step[i])
	}
	y := make([]float32, l.out)
	for o := range y {
		v := float32(dot8(l.w[o*l.in:], q))*l.scale[o] + l.bias[o]
		if act {
			v = gelu(v)
		}
		y[o] = v
	}
	return y
}

func gelu(x float32) float32 {
	return 0.5 * x * (1 + float32(math.Erf(float64(x*0.70710678))))
}

func q8(x, step float32) int8 {
	q := float32(math.RoundToEven(float64(x / step)))
	return int8(min(max(q, -127), 127))
}

// dot8 sums w·x over len(x) entries.
func dot8(w, x []int8) int32 {
	var acc int32
	for i, v := range x {
		acc += int32(w[i]) * int32(v)
	}
	return acc
}
