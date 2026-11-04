package sessionidx

import (
	"bytes"
	"encoding/json"
	"time"
)

// jsonDecoder builds a json.Decoder with UseNumber to avoid float garbage.
func jsonDecoder(b []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return dec
}

// jsonTime is a helper for tolerant timestamp parsing.
func jsonTime(s string) time.Time {
	var t time.Time
	_ = t.UnmarshalText([]byte(s))
	return t
}
