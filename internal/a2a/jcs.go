package a2a

// RFC 8785 (JSON Canonicalization Scheme, JCS) canonicalization for Agent
// Card signature verification (A2A spec §8.4).
//
// The A2A spec signs the canonicalized card JSON, so the verifier must
// reproduce the exact canonical form the signer produced: sorted object keys
// (UTF-16 code-unit order), ECMAScript Number::toString number formatting,
// and JSON.stringify-compatible string escaping. This file implements that
// subset of JCS needed for cards; it is self-contained to avoid pulling in an
// external canonicalization dependency.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// jcsCanonicalize returns the RFC 8785 canonical form of the JSON value in raw.
func jcsCanonicalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("a2a jcs: decode: %w", err)
	}
	var buf bytes.Buffer
	if err := jcsEncode(&buf, v); err != nil {
		return nil, fmt.Errorf("a2a jcs: %w", err)
	}
	return buf.Bytes(), nil
}

func jcsEncode(buf *bytes.Buffer, v interface{}) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		jcsString(buf, t)
	case json.Number:
		s, err := jcsNumber(string(t))
		if err != nil {
			return err
		}
		buf.WriteString(s)
	case []interface{}:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := jcsEncode(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return jcsKeyLess(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			jcsString(buf, k)
			buf.WriteByte(':')
			if err := jcsEncode(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("unsupported type %T", v)
	}
	return nil
}

// jcsKeyLess orders strings by their UTF-16 code unit sequence, as required by
// RFC 8785 §3.2.3 (matches ECMAScript array sort on strings).
func jcsKeyLess(a, b string) bool {
	au := utf16.Encode([]rune(a))
	bu := utf16.Encode([]rune(b))
	n := len(au)
	if len(bu) < n {
		n = len(bu)
	}
	for i := 0; i < n; i++ {
		if au[i] != bu[i] {
			return au[i] < bu[i]
		}
	}
	return len(au) < len(bu)
}

// jcsString escapes per ECMAScript JSON.stringify: only quote, backslash and
// C0 control characters are escaped; all other runes are emitted as raw UTF-8.
func jcsString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			buf.WriteString(`\"`)
		case r == '\\':
			buf.WriteString(`\\`)
		case r == '\b':
			buf.WriteString(`\b`)
		case r == '\f':
			buf.WriteString(`\f`)
		case r == '\n':
			buf.WriteString(`\n`)
		case r == '\r':
			buf.WriteString(`\r`)
		case r == '\t':
			buf.WriteString(`\t`)
		case r >= 0 && r < 0x20:
			const hexdigits = "0123456789abcdef"
			buf.WriteString(`\u00`)
			buf.WriteByte(hexdigits[r>>4])
			buf.WriteByte(hexdigits[r&0xF])
		default:
			buf.WriteString(s[i : i+size])
		}
		i += size
	}
	buf.WriteByte('"')
}

// jcsNumber formats a JSON number the way ECMAScript Number::toString does
// (RFC 8785 §3.2.2.3): fixed notation for |x| in [1e-6, 1e21), scientific
// notation otherwise with no leading zeros in the exponent.
func jcsNumber(n string) (string, error) {
	f, err := strconv.ParseFloat(n, 64)
	if err != nil {
		return "", fmt.Errorf("number %q: %w", n, err)
	}
	if f == 0 {
		// 0 and -0 serialize as "0" (RFC 8785 §3.2.2.3.1).
		return "0", nil
	}
	abs := math.Abs(f)
	if abs < 1e-6 || abs >= 1e21 {
		b := strconv.AppendFloat(nil, f, 'e', -1, 64)
		ei := bytes.IndexByte(b, 'e')
		if ei < 0 {
			return "", fmt.Errorf("number %q: missing exponent", n)
		}
		exp := strings.TrimLeft(string(b[ei+2:]), "0")
		if exp == "" {
			exp = "0"
		}
		return string(b[:ei+1]) + string(b[ei+1]) + exp, nil
	}
	return strconv.FormatFloat(f, 'f', -1, 64), nil
}
