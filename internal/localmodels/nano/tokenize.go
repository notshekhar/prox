package nano

import "unicode/utf8"

// Tokenizer limits, as in the reference nano_text.c.
const (
	maxTokens     = 512           // tokens kept per text; the rest is dropped
	maxWord       = 100           // longer words become the unknown token
	maxCodePoints = 4 * maxTokens // characters read per text
)

// tokenize is nano_text.c's BERT-style word-piece tokenizer: fold case and
// accents, split on spaces and punctuation, then greedy longest-match pieces.
// It reports whether the text was cut short.
func (m *Model) tokenize(text string) ([]int32, bool) {
	cps, cut := decodeUTF8(text)
	t := tokenizer{m: m, ids: make([]int32, 0, 64)}
	for _, c := range cps {
		if len(t.ids) >= maxTokens {
			cut = true
			break
		}
		switch {
		case c == 0 || isControl(c):
		case isSpace(c):
			t.flush()
		case isCombining(c):
		default:
			t.add(normal(c))
		}
	}
	t.flush() // the reference flushes the pending word even when full
	return t.ids, cut
}

type tokenizer struct {
	m        *Model
	ids      []int32
	word     []rune
	overflow bool
}

func (t *tokenizer) add(c rune) {
	if isPunct(c) || isCJK(c) {
		t.flush()
		t.wordpiece([]rune{c})
		return
	}
	if len(t.word) < maxWord+1 {
		t.word = append(t.word, c)
	} else {
		t.overflow = true
	}
}

func (t *tokenizer) flush() {
	if len(t.word) > 0 {
		if t.overflow {
			t.pushOverflow()
		} else {
			t.wordpiece(t.word)
		}
	}
	t.word, t.overflow = t.word[:0], false
}

// pushOverflow records a word too long to keep; when the text is already
// full, the reference overwrites the last token instead.
func (t *tokenizer) pushOverflow() {
	if len(t.ids) < maxTokens {
		t.ids = append(t.ids, int32(t.m.unk))
		return
	}
	t.ids[maxTokens-1] = int32(t.m.unk)
}

func (t *tokenizer) push(id int32) {
	if len(t.ids) < maxTokens {
		t.ids = append(t.ids, id)
	}
}

// wordpiece splits one word into the longest vocabulary pieces, "##" marking
// a continuation; a word with an unknown piece becomes one unknown token.
func (t *tokenizer) wordpiece(word []rune) {
	if len(t.ids) >= maxTokens {
		return
	}
	if len(word) > maxWord {
		t.push(int32(t.m.unk))
		return
	}
	var pieces []int32
	buf := make([]byte, 0, 4*maxWord+2)
	for start := 0; start < len(word); {
		found, end := int32(-1), len(word)
		for ; end > start; end-- {
			buf = buf[:0]
			if start > 0 {
				buf = append(buf, '#', '#')
			}
			for _, c := range word[start:end] {
				buf = utf8.AppendRune(buf, c)
			}
			if found = t.m.lookup(buf); found >= 0 {
				break
			}
		}
		if found < 0 {
			t.push(int32(t.m.unk))
			return
		}
		pieces = append(pieces, found)
		start = end
	}
	for _, id := range pieces {
		t.push(id)
	}
}

// decodeUTF8 reads up to maxCodePoints characters, turning malformed bytes
// into U+FFFD the way the reference decoder does.
func decodeUTF8(s string) ([]rune, bool) {
	out := make([]rune, 0, min(len(s), maxCodePoints))
	for i := 0; i < len(s); {
		if len(out) == maxCodePoints {
			return out, true
		}
		c, size := decodeOne(s[i:])
		out = append(out, c)
		i += size
	}
	return out, false
}

// decodeOne mirrors nano_text.c: it trusts the lead byte's length and does not
// check continuation bytes, so token ids match the reference on any input.
func decodeOne(s string) (rune, int) {
	c := rune(s[0])
	n := 1
	switch {
	case c < 0x80:
	case c>>5 == 6:
		n = 2
	case c>>4 == 14:
		n = 3
	case c>>3 == 30:
		n = 4
	}
	if n == 1 {
		return c, 1
	}
	if n > len(s) {
		return 0xFFFD, 1
	}
	c &= 0xFF >> (n + 1)
	for j := 1; j < n; j++ {
		c = c<<6 | rune(s[j]&0x3F)
	}
	return c, n
}

var latin1Fold = [64]rune{
	'a', 'a', 'a', 'a', 'a', 'a', 0xE6, 'c', 'e', 'e', 'e', 'e', 'i', 'i', 'i', 'i', 0xF0, 'n', 'o', 'o', 'o', 'o', 'o', 0xD7, 0xF8, 'u', 'u', 'u', 'u', 'y', 0xFE, 0xDF,
	'a', 'a', 'a', 'a', 'a', 'a', 0xE6, 'c', 'e', 'e', 'e', 'e', 'i', 'i', 'i', 'i', 0xF0, 'n', 'o', 'o', 'o', 'o', 'o', 0xF7, 0xF8, 'u', 'u', 'u', 'u', 'y', 0xFE, 'y',
}

func fold(c rune) rune {
	switch {
	case c >= 'A' && c <= 'Z':
		return c + 32
	case c >= 0xC0 && c <= 0xFF:
		return latin1Fold[c-0xC0]
	case c >= 0x391 && c <= 0x3A9, c >= 0x410 && c <= 0x42F:
		return c + 32
	case c >= 0x400 && c <= 0x40F:
		return c + 80
	}
	return c
}

var latinExtA = []struct {
	lo, hi rune
	base   rune
}{
	{0x100, 0x105, 'a'}, {0x106, 0x10D, 'c'}, {0x10E, 0x111, 'd'}, {0x112, 0x11B, 'e'}, {0x11C, 0x123, 'g'}, {0x124, 0x127, 'h'},
	{0x128, 0x12F, 'i'}, {0x134, 0x135, 'j'}, {0x136, 0x137, 'k'}, {0x139, 0x142, 'l'}, {0x143, 0x148, 'n'}, {0x14C, 0x151, 'o'},
	{0x154, 0x159, 'r'}, {0x15A, 0x161, 's'}, {0x162, 0x165, 't'}, {0x168, 0x173, 'u'}, {0x174, 0x175, 'w'}, {0x176, 0x178, 'y'},
	{0x179, 0x17E, 'z'}, {0x17F, 0x17F, 's'},
}

func foldLatinExtA(c rune) rune {
	for _, r := range latinExtA {
		if c >= r.lo && c <= r.hi {
			return r.base
		}
	}
	switch c {
	case 0x130:
		return 'i'
	case 0x132, 0x133:
		return 0x133
	case 0x14A, 0x14B:
		return 0x14B
	case 0x152, 0x153:
		return 0x153
	case 0x166, 0x167:
		return 0x167
	}
	return c
}

func normal(c rune) rune {
	if c >= 0x100 && c <= 0x17F {
		return foldLatinExtA(c)
	}
	return fold(c)
}

func isCombining(c rune) bool { return c >= 0x300 && c <= 0x36F }

func isSpace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == 0xA0 || c == 0x1680 || (c >= 0x2000 && c <= 0x200A) ||
		c == 0x202F || c == 0x205F || c == 0x3000
}

func isControl(c rune) bool {
	if c == '\t' || c == '\n' || c == '\r' {
		return false
	}
	return c < 0x20 || (c >= 0x7F && c < 0xA0) || c == 0xFFFD || (c >= 0x200B && c <= 0x200F) || c == 0xFEFF || c == 0xAD
}

func isPunct(c rune) bool {
	if (c >= 33 && c <= 47) || (c >= 58 && c <= 64) || (c >= 91 && c <= 96) || (c >= 123 && c <= 126) {
		return true
	}
	switch c {
	case 0xA1, 0xA7, 0xAB, 0xB6, 0xB7, 0xBB, 0xBF, 0x37E, 0x387:
		return true
	}
	return (c >= 0x2010 && c <= 0x2027) || (c >= 0x2030 && c <= 0x2043) || (c >= 0x2045 && c <= 0x2051) ||
		(c >= 0x2053 && c <= 0x205E) || (c >= 0x3001 && c <= 0x3003) || (c >= 0x3008 && c <= 0x3011)
}

func isCJK(c rune) bool {
	return (c >= 0x4E00 && c <= 0x9FFF) || (c >= 0x3400 && c <= 0x4DBF) || (c >= 0x20000 && c <= 0x2A6DF) ||
		(c >= 0xF900 && c <= 0xFAFF) || (c >= 0x2F800 && c <= 0x2FA1F)
}
