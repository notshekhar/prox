// Package nano runs Julia 2 Nano's text decisions in pure Go: a port of
// Supersonic Labs' nano_text.c (Apache-2.0). Each option gets one score from a
// small int8 network over hashed word-piece embeddings; nothing is generated.
package nano

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
)

const (
	dim      = 256 // width of one embedding table
	hidden   = 256 // width of the scoring network
	qtypeDim = 32  // width of the question-type embedding
	hashSize = 1 << 16
	noSlot   = 0xFFFF
	magic    = "JNT3"

	int4RowBytes = 8 * 18 // dim/32 groups of an f16 scale + 16 bytes of nibbles
	int2RowBytes = 8 * 10 // dim/32 groups of an f16 scale + 8 bytes of crumbs
)

// QuestionType is the reference's question code.
type QuestionType uint32

const (
	Choice QuestionType = 0
	YesNo  QuestionType = 1
	Score  QuestionType = 2
)

type layer struct {
	out, in           int
	step, scale, bias []float32
	w                 []int8
}

// Model is a loaded nano_text.bin. It is read-only after Load, so one Model
// serves any number of goroutines.
type Model struct {
	blob     []byte
	unk      uint32
	tables   int // 1 or 2 embedding tables, concatenated
	vd       int // tables * dim
	tokOff   []uint32
	hashSlot [hashSize]uint16
	tableA   []byte
	slotB    []uint16
	rows4B   []byte
	rows2B   []byte
	qtypeEmb []float32
	layers   [3]layer
}

// Load reads and checks a nano_text.bin.
func Load(path string) (*Model, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(blob)
}

// Parse reads the JNT3 layout written for nano_text.c.
func Parse(blob []byte) (*Model, error) {
	r := &reader{b: blob}
	if !bytes.Equal(r.bytes(4), []byte(magic)) {
		return nil, errors.New("not a Julia 2 Nano text model")
	}
	m := &Model{blob: blob}
	vocab := int(r.u32())
	if r.u32() != dim || vocab >= noSlot {
		return nil, errors.New("unexpected width or vocabulary size")
	}
	m.unk = r.u32()
	m.tables = int(r.u32())
	if m.tables != 1 && m.tables != 2 {
		return nil, fmt.Errorf("unexpected table count %d", m.tables)
	}
	m.vd = m.tables * dim
	m.readVocabulary(r, vocab)
	m.readTables(r, vocab)
	m.qtypeEmb = r.f32s(3 * qtypeDim)
	for i := range m.layers {
		m.layers[i] = r.layer()
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(blob) {
		return nil, fmt.Errorf("size mismatch: read %d of %d bytes", r.off, len(blob))
	}
	return m, nil
}

func (m *Model) readVocabulary(r *reader, vocab int) {
	m.tokOff = make([]uint32, vocab)
	for i := range m.hashSlot {
		m.hashSlot[i] = noSlot
	}
	for i := range vocab {
		m.tokOff[i] = uint32(r.off)
		piece := r.bytes(int(r.u16()))
		h := fnv(piece) & (hashSize - 1)
		for m.hashSlot[h] != noSlot {
			h = (h + 1) & (hashSize - 1)
		}
		m.hashSlot[h] = uint16(i)
	}
}

func (m *Model) readTables(r *reader, vocab int) {
	m.tableA = r.bytes(vocab * int4RowBytes)
	if m.tables != 2 {
		return
	}
	m.slotB = make([]uint16, vocab)
	n4 := 0
	for i := range m.slotB {
		m.slotB[i] = r.u16()
		if m.slotB[i]&0x8000 != 0 {
			n4++
		}
	}
	m.rows4B = r.bytes(n4 * int4RowBytes)
	m.rows2B = r.bytes((vocab - n4) * int2RowBytes)
}

func fnv(s []byte) uint32 {
	h := uint32(2166136261)
	for _, c := range s {
		h = (h ^ uint32(c)) * 16777619
	}
	return h
}

// lookup finds a word piece's id, or -1.
func (m *Model) lookup(piece []byte) int32 {
	for h := fnv(piece) & (hashSize - 1); ; h = (h + 1) & (hashSize - 1) {
		id := m.hashSlot[h]
		if id == noSlot {
			return -1
		}
		off := m.tokOff[id]
		n := int(binary.LittleEndian.Uint16(m.blob[off:]))
		if n == len(piece) && bytes.Equal(m.blob[off+2:int(off)+2+n], piece) {
			return int32(id)
		}
	}
}

// reader walks the little-endian blob; the first short read sticks in err.
type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) bytes(n int) []byte {
	if r.err != nil || n < 0 || r.off+n > len(r.b) {
		if r.err == nil {
			r.err = errors.New("model file is truncated")
		}
		return make([]byte, max(n, 0))
	}
	out := r.b[r.off : r.off+n]
	r.off += n
	return out
}

func (r *reader) u16() uint16 { return binary.LittleEndian.Uint16(r.bytes(2)) }
func (r *reader) u32() uint32 { return binary.LittleEndian.Uint32(r.bytes(4)) }

func (r *reader) f32s(n int) []float32 {
	raw := r.bytes(4 * n)
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return out
}

func (r *reader) layer() layer {
	l := layer{out: int(r.u32()), in: int(r.u32())}
	l.step = r.f32s(l.in)
	l.scale = r.f32s(l.out)
	l.bias = r.f32s(l.out)
	raw := r.bytes(l.out * l.in)
	l.w = make([]int8, len(raw))
	for i, b := range raw {
		l.w[i] = int8(b)
	}
	return l
}
