// Ordered-JSON re-stringification: TS JSON.parse preserves source key order
// (insertion order) and JSON.stringify re-emits in that order. Go maps do
// not — json.Marshal sorts keys alphabetically. For parseChatRequest's
// non-string content branch this matters: content strings feed token
// estimation, Jev excerpts, and the session-id hash. This file re-emits
// compact JSON preserving source order.
package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// orderedPair is one object entry preserved in source order.
type orderedPair struct {
	Key   string
	Value any
}

// orderedReader decodes JSON preserving object key order.
type orderedReader struct {
	dec *json.Decoder
}

func newOrderedReader(data []byte) *orderedReader {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return &orderedReader{dec: dec}
}

// readValue reads one value (recursively), preserving object key order.
func (r *orderedReader) readValue() (any, error) {
	tok, err := r.dec.Token()
	if err != nil {
		return nil, err
	}
	return r.readFromTok(tok)
}

func (r *orderedReader) readFromTok(tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := []orderedPair{}
			for r.dec.More() {
				keyTok, err := r.dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("object key not a string")
				}
				val, err := r.readValue()
				if err != nil {
					return nil, err
				}
				obj = append(obj, orderedPair{Key: key, Value: val})
			}
			if _, err := r.dec.Token(); err != nil { // consume '}'
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for r.dec.More() {
				val, err := r.readValue()
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := r.dec.Token(); err != nil { // consume ']'
				return nil, err
			}
			return arr, nil
		default:
			return nil, fmt.Errorf("unexpected delim %v", t)
		}
	default:
		// leaf: string, json.Number, bool, nil
		return tok, nil
	}
}

// reStringify mirrors JSON.stringify(JSON.parse(x)): same key order, no
// whitespace, JS-compatible escaping.
func reStringify(data []byte) (string, error) {
	r := newOrderedReader(data)
	val, err := r.readValue()
	if err != nil {
		return "", err
	}
	if _, err := r.dec.Token(); err != io.EOF { // trailing garbage check
		return "", fmt.Errorf("trailing data after JSON value")
	}
	var buf bytes.Buffer
	if err := encodeOrdered(&buf, val); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// patchModelInBody mirrors TS `{ ...rawBody, model: id }`: re-stringify the
// ordered body with "model" replaced IN ITS ORIGINAL POSITION (TS object
// spread overwrites in place when the key exists; appends at the end when it
// doesn't).
func patchModelInBody(rawBodyJSON []byte, modelID string) json.RawMessage {
	reader := newOrderedReader(rawBodyJSON)
	top, err := reader.readValue()
	if err != nil {
		return json.RawMessage(`{"model":"` + modelID + `"}`)
	}
	pairs, ok := top.([]orderedPair)
	if !ok {
		return json.RawMessage(`{"model":"` + modelID + `"}`)
	}
	out := make([]orderedPair, 0, len(pairs)+1)
	found := false
	for _, p := range pairs {
		if p.Key == "model" {
			out = append(out, orderedPair{Key: "model", Value: modelID})
			found = true
		} else {
			out = append(out, p)
		}
	}
	if !found {
		out = append(out, orderedPair{Key: "model", Value: modelID})
	}
	var buf bytes.Buffer
	if err := encodeOrdered(&buf, out); err != nil {
		return json.RawMessage(`{"model":"` + modelID + `"}`)
	}
	return json.RawMessage(buf.Bytes())
}

// encodeOrdered writes val compactly with objects in their pair order.
func encodeOrdered(buf *bytes.Buffer, val any) error {
	switch t := val.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		// json.Number preserves the source token, matching JS parse→stringify
		// for all round-trippable numbers.
		buf.WriteString(t.String())
	case string:
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false) // JS does not escape HTML chars
		if err := enc.Encode(t); err != nil {
			return err
		}
		if b := buf.Bytes(); len(b) > 0 && b[len(b)-1] == '\n' {
			buf.Truncate(buf.Len() - 1)
		}
	case []orderedPair:
		buf.WriteByte('{')
		for i, p := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeOrdered(buf, p.Key); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := encodeOrdered(buf, p.Value); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, v := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeOrdered(buf, v); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	return nil
}