package nano

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
)

// ImageSide is the photo size the image model reads: 128 × 128 RGB.
const ImageSide = 128

const imageMagic = "JNI1"

// Image network ops, as nano_image.c numbers them.
const (
	opConv   = 1
	opSE     = 2 // squeeze-and-excitation gate
	opPush   = 3 // keep the activations for a residual add
	opAdd    = 4
	opPool   = 5 // global average
	opLinear = 6
)

// gridLimit is the int16 range activations are rounded into before each layer.
const gridLimit = 32767

var (
	imageMean = [3]float32{0.485, 0.456, 0.406}
	imageStd  = [3]float32{0.229, 0.224, 0.225}
)

type imageOp struct {
	kind   uint32
	conv   *convOp
	se     *seOp
	linear *denseOp
}

type convOp struct {
	cin, cout, k, stride, groups, act int
	step                              float32
	scale, bias                       []float32
	w                                 []int8
}

type denseOp struct {
	in, out, act int
	step         float32
	scale, bias  []float32
	w            []int8
}

type seOp struct{ squeeze, excite denseOp }

// ImageModel is a loaded nano_image.bin: a small int8 CNN that scores the
// labels in labels/image.json. It is read-only after loading.
type ImageModel struct{ ops []imageOp }

// LoadImageModel reads and checks a nano_image.bin.
func LoadImageModel(path string) (*ImageModel, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseImageModel(blob)
}

// ParseImageModel reads the JNI1 layout written for nano_image.c.
func ParseImageModel(blob []byte) (*ImageModel, error) {
	r := &reader{b: blob}
	if !bytes.Equal(r.bytes(4), []byte(imageMagic)) {
		return nil, errors.New("not a Julia 2 Nano image model")
	}
	n := int(r.u32())
	m := &ImageModel{ops: make([]imageOp, 0, n)}
	for range n {
		op, err := readImageOp(r)
		if err != nil {
			return nil, err
		}
		m.ops = append(m.ops, op)
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(blob) {
		return nil, fmt.Errorf("size mismatch: read %d of %d bytes", r.off, len(blob))
	}
	return m, nil
}

func readImageOp(r *reader) (imageOp, error) {
	op := imageOp{kind: r.u32()}
	switch op.kind {
	case opConv:
		op.conv = readConv(r)
	case opSE:
		op.se = readSE(r)
	case opLinear:
		in, out, act := int(r.u32()), int(r.u32()), int(r.u32())
		d := readDense(r, in, out, act)
		op.linear = &d
	case opPush, opAdd, opPool:
	default:
		if r.err != nil {
			return op, r.err
		}
		return op, fmt.Errorf("unknown image op %d", op.kind)
	}
	return op, nil
}

func readConv(r *reader) *convOp {
	c := &convOp{cin: int(r.u32()), cout: int(r.u32()), k: int(r.u32()), stride: int(r.u32()), groups: int(r.u32()), act: int(r.u32())}
	c.step = r.f32s(1)[0]
	c.scale = r.f32s(c.cout)
	c.bias = r.f32s(c.cout)
	if c.groups <= 0 {
		r.err = errors.New("conv with no groups")
		return c
	}
	c.w = int8s(r.bytes(c.cout * (c.cin / c.groups) * c.k * c.k))
	return c
}

// readDense reads a step, per-output scale and bias, then int8 weights.
func readDense(r *reader, in, out, act int) denseOp {
	d := denseOp{in: in, out: out, act: act}
	d.step = r.f32s(1)[0]
	d.scale = r.f32s(out)
	d.bias = r.f32s(out)
	d.w = int8s(r.bytes(out * in))
	return d
}

func readSE(r *reader) *seOp {
	ch, mid := int(r.u32()), int(r.u32())
	return &seOp{squeeze: readDense(r, ch, mid, actReLU), excite: readDense(r, mid, ch, actNone)}
}

func int8s(raw []byte) []int8 {
	out := make([]int8, len(raw))
	for i, b := range raw {
		out[i] = int8(b)
	}
	return out
}

// Activations, as nano_image.c numbers them.
const (
	actNone      = 0
	actReLU      = 1
	actHardSwish = 2
)

func activate(x float32, kind int) float32 {
	switch kind {
	case actReLU:
		return max(x, 0)
	case actHardSwish:
		return x * min(max(x+3, 0), 6) / 6
	}
	return x
}

func hardSigmoid(x float32) float32 { return min(max(x+3, 0), 6) / 6 }

// grid rounds x onto the layer's int16 quantisation grid.
func grid(x, step float32) float32 {
	q := float32(math.RoundToEven(float64(x / step)))
	return min(max(q, -gridLimit), gridLimit) * step
}

// tensor is a channels × height × width activation.
type tensor struct {
	c, h, w int
	v       []float32
}

// Logits scores one 128 × 128 photo, rgb row by row, three bytes a pixel.
func (m *ImageModel) Logits(rgb []byte) ([]float32, error) {
	if len(rgb) != ImageSide*ImageSide*3 {
		return nil, fmt.Errorf("want %d RGB bytes, got %d", ImageSide*ImageSide*3, len(rgb))
	}
	cur := normalisePixels(rgb)
	var kept tensor
	for _, op := range m.ops {
		switch op.kind {
		case opConv:
			cur = op.conv.apply(cur)
		case opSE:
			op.se.apply(cur)
		case opPush:
			kept = tensor{cur.c, cur.h, cur.w, append([]float32(nil), cur.v...)}
		case opAdd:
			if kept.c != cur.c || kept.h != cur.h || kept.w != cur.w {
				return nil, errors.New("residual shape mismatch")
			}
			for i := range cur.v {
				cur.v[i] += kept.v[i]
			}
		case opPool:
			cur = tensor{cur.c, 1, 1, channelMeans(cur)}
		case opLinear:
			cur = tensor{op.linear.out, 1, 1, op.linear.apply(cur.v)}
		}
	}
	return cur.v, nil
}

func normalisePixels(rgb []byte) tensor {
	t := tensor{3, ImageSide, ImageSide, make([]float32, 3*ImageSide*ImageSide)}
	for y := range ImageSide {
		for x := range ImageSide {
			for ch := range 3 {
				v := float32(rgb[(y*ImageSide+x)*3+ch])/255 - imageMean[ch]
				t.v[ch*ImageSide*ImageSide+y*ImageSide+x] = v / imageStd[ch]
			}
		}
	}
	return t
}

// channelMeans averages each channel in float64, as the reference does.
func channelMeans(t tensor) []float32 {
	out := make([]float32, t.c)
	n := t.h * t.w
	for ch := range out {
		var s float64
		for _, v := range t.v[ch*n : (ch+1)*n] {
			s += float64(v)
		}
		out[ch] = float32(s / float64(n))
	}
	return out
}

func (c *convOp) apply(in tensor) tensor {
	pad := c.k / 2
	oh, ow := (in.h+2*pad-c.k)/c.stride+1, (in.w+2*pad-c.k)/c.stride+1
	q := make([]float32, len(in.v))
	for i, v := range in.v {
		q[i] = grid(v, c.step)
	}
	cpg, opg := c.cin/c.groups, c.cout/c.groups
	out := tensor{c.cout, oh, ow, make([]float32, c.cout*oh*ow)}
	for o := range c.cout {
		g := o / opg
		wo := c.w[o*cpg*c.k*c.k:]
		for y := range oh {
			for x := range ow {
				acc := c.dot(q, in, wo, g*cpg, cpg, y, x, pad)
				out.v[o*oh*ow+y*ow+x] = activate(acc*c.scale[o]+c.bias[o], c.act)
			}
		}
	}
	return out
}

// dot is one output pixel: the kernel over cpg input channels from first.
func (c *convOp) dot(q []float32, in tensor, wo []int8, first, cpg, y, x, pad int) float32 {
	var acc float32
	for ci := range cpg {
		plane := q[(first+ci)*in.h*in.w:]
		wk := wo[ci*c.k*c.k:]
		for ky := range c.k {
			iy := y*c.stride + ky - pad
			if iy < 0 || iy >= in.h {
				continue
			}
			for kx := range c.k {
				ix := x*c.stride + kx - pad
				if ix < 0 || ix >= in.w {
					continue
				}
				acc += float32(wk[ky*c.k+kx]) * plane[iy*in.w+ix]
			}
		}
	}
	return acc
}

func (d *denseOp) apply(x []float32) []float32 {
	q := make([]float32, d.in)
	for i := range q {
		q[i] = grid(x[i], d.step)
	}
	out := make([]float32, d.out)
	for o := range out {
		var acc float32
		w := d.w[o*d.in : (o+1)*d.in]
		for i, v := range q {
			acc += float32(w[i]) * v
		}
		out[o] = activate(acc*d.scale[o]+d.bias[o], d.act)
	}
	return out
}

func (s *seOp) apply(t tensor) {
	gate := s.excite.apply(s.squeeze.apply(channelMeans(t)))
	n := t.h * t.w
	for ch := range t.c {
		g := hardSigmoid(gate[ch])
		for i := range n {
			t.v[ch*n+i] *= g
		}
	}
}
