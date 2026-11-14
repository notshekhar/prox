package jsoncompat

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"strconv"
	"time"
)

// v2 has no default representation for time.Duration; v1 used the integer
// number of nanoseconds, which is what stored state and API responses hold.
var (
	durationMarshalers = json.MarshalToFunc(func(enc *jsontext.Encoder, d time.Duration) error {
		return enc.WriteToken(jsontext.Int(int64(d)))
	})
	durationUnmarshalers = json.UnmarshalFromFunc(unmarshalDuration)
)

func unmarshalDuration(dec *jsontext.Decoder, d *time.Duration) error {
	switch dec.PeekKind() {
	case 'n':
		// v1 left the value untouched on null.
		_, err := dec.ReadToken()
		return err
	case '0':
		raw, err := dec.ReadValue()
		if err != nil {
			return err
		}
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return err
		}
		*d = time.Duration(n)
		return nil
	default:
		return errors.ErrUnsupported
	}
}
