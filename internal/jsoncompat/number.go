package jsoncompat

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
)

// Number is a JSON number kept as its literal text. It replaces v1's
// json.Number: DecodeUseNumber produces it, and it encodes back as the same
// number literal rather than as a string.
type Number string

// String returns the literal text of the number.
func (n Number) String() string { return string(n) }

// Float64 returns the number as a float64.
func (n Number) Float64() (float64, error) { return strconv.ParseFloat(string(n), 64) }

// Int64 returns the number as an int64.
func (n Number) Int64() (int64, error) { return strconv.ParseInt(string(n), 10, 64) }

// MarshalJSON writes the literal unchanged. Like v1, an empty Number encodes
// as 0 and text that is not a JSON number is an error.
func (n Number) MarshalJSON() ([]byte, error) {
	if n == "" {
		return []byte("0"), nil
	}
	if v := jsontext.Value(n); v.Kind() != '0' || !v.IsValid() {
		return nil, fmt.Errorf("jsoncompat: invalid number literal %q", string(n))
	}
	return []byte(n), nil
}

var numberUnmarshalers = json.UnmarshalFromFunc(unmarshalNumberIntoAny)

// unmarshalNumberIntoAny stores JSON numbers decoded into an interface value
// as Number; every other kind falls through to the default decoding.
func unmarshalNumberIntoAny(dec *jsontext.Decoder, v *any) error {
	if dec.PeekKind() != '0' {
		return errors.ErrUnsupported
	}
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	*v = Number(raw)
	return nil
}
