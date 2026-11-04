package sessionidx

import (
	"bytes"
	"encoding/json"
)

// jsonDecoder builds a json.Decoder with UseNumber to avoid float garbage.
func jsonDecoder(b []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return dec
}
