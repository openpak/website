package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
)

// DecodeBundle parses a policy bundle with the strictness PRD 01 requires:
// duplicate JSON keys and non-finite numbers reject the whole document, and
// every unknown field rejects it too — except x_-prefixed diagnostic
// extensions, which a consumer may ignore. Validation (address classes,
// cross-references, limits) is a separate step: decode, then Validate.
func DecodeBundle(data []byte) (*Bundle, error) {
	if len(data) > MaxPolicyBytes {
		return nil, fmt.Errorf("policy bundle is %d bytes, over the %d byte limit", len(data), MaxPolicyBytes)
	}
	var b Bundle
	if err := strictDecode(data, &b); err != nil {
		return nil, fmt.Errorf("policy bundle: %w", err)
	}
	return &b, nil
}

// DecodeManifest parses a bundle manifest under the same strictness.
func DecodeManifest(data []byte) (*Manifest, error) {
	if len(data) > MaxManifestBytes {
		return nil, fmt.Errorf("manifest is %d bytes, over the %d byte limit", len(data), MaxManifestBytes)
	}
	var m Manifest
	if err := strictDecode(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return &m, nil
}

// strictDecode round-trips the document through two passes before handing it
// to encoding/json: a whole-document duplicate-key walk (encoding/json would
// silently keep the last write), and a reflection-guided walk that knows
// which fields the target type carries. Numbers stay json.Number until the
// final unmarshal, so an out-of-range or fractional value fails where it is
// typed, not here.
func strictDecode(data []byte, v any) error {
	if err := checkDuplicateKeys(data); err != nil {
		return err
	}
	if err := checkUnknownFields(data, reflect.TypeOf(v)); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// checkDuplicateKeys walks every object of the document and rejects a key
// that appears twice in the same object, at any depth, including inside
// diagnostic extensions — the PRD's rule is about the bytes, not about
// whether we understood them.
func checkDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dupWalk(dec)
}

func dupWalk(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil // a scalar: nothing to duplicate
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key := keyTok.(string)
			if seen[key] {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = true
			if err := dupWalk(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token() // closing '}'
		return err
	case '[':
		for dec.More() {
			if err := dupWalk(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token() // closing ']'
		return err
	default:
		return fmt.Errorf("unexpected delimiter %q", d)
	}
}

// checkUnknownFields walks the document alongside the Go type it will be
// decoded into and rejects any key the type does not carry, unless the key
// is an x_-prefixed diagnostic extension. Unknown security semantics are a
// rejection, never a silent skip (PRD 01).
func checkUnknownFields(data []byte, t reflect.Type) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	w := &fieldWalker{dec: dec}
	if err := w.value(t); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing data after JSON document")
	}
	return nil
}

type fieldWalker struct {
	dec *json.Decoder
}

func (w *fieldWalker) value(t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if isTimeType(t) {
		_, err := w.dec.Token() // a string, consumed
		return err
	}
	switch t.Kind() {
	case reflect.Struct:
		return w.object(t)
	case reflect.Slice:
		return w.array(t.Elem())
	case reflect.Map:
		return w.stringMap(t.Elem())
	default:
		tok, err := w.dec.Token()
		if err != nil {
			return err
		}
		if _, ok := tok.(json.Delim); ok {
			return fmt.Errorf("expected a scalar, found a composite")
		}
		return nil
	}
}

func (w *fieldWalker) object(t reflect.Type) error {
	keys := structKeys(t)
	tok, err := w.dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("expected an object")
	}
	for w.dec.More() {
		keyTok, err := w.dec.Token()
		if err != nil {
			return err
		}
		key := keyTok.(string)
		ft, known := keys[key]
		if !known {
			if strings.HasPrefix(key, "x_") {
				if err := w.skip(); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("unknown field %q", key)
		}
		if err := w.value(ft); err != nil {
			return err
		}
	}
	_, err = w.dec.Token() // closing '}'
	return err
}

func (w *fieldWalker) array(elem reflect.Type) error {
	tok, err := w.dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return fmt.Errorf("expected an array")
	}
	for w.dec.More() {
		if err := w.value(elem); err != nil {
			return err
		}
	}
	_, err = w.dec.Token() // closing ']'
	return err
}

func (w *fieldWalker) stringMap(elem reflect.Type) error {
	tok, err := w.dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("expected an object")
	}
	for w.dec.More() {
		if _, err := w.dec.Token(); err != nil { // the key
			return err
		}
		if err := w.value(elem); err != nil {
			return err
		}
	}
	_, err = w.dec.Token() // closing '}'
	return err
}

// skip consumes one whole value without interpreting it: the x_ extension
// path. Its interior is still checked for duplicate keys by the earlier walk.
func (w *fieldWalker) skip() error {
	tok, err := w.dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		for w.dec.More() {
			if _, err := w.dec.Token(); err != nil {
				return err
			}
			if err := w.skip(); err != nil {
				return err
			}
		}
		_, err = w.dec.Token()
		return err
	case '[':
		for w.dec.More() {
			if err := w.skip(); err != nil {
				return err
			}
		}
		_, err = w.dec.Token()
		return err
	default:
		return fmt.Errorf("unexpected delimiter %q", d)
	}
}

// structKeys maps JSON names to field types for one struct type.
func structKeys(t reflect.Type) map[string]reflect.Type {
	keys := make(map[string]reflect.Type, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("json"); ok {
			name = strings.Split(tag, ",")[0]
			if name == "-" {
				continue
			}
		}
		keys[name] = f.Type
	}
	return keys
}

func isTimeType(t reflect.Type) bool {
	return t == reflect.TypeOf(time.Time{})
}
