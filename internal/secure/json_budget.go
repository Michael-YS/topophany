package secure

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// JSONBudget checks representation cost before unmarshalling into Go objects.
// Only the bounded parser stack and object keys are retained during this pass.
// Duplicate keys are rejected so validation and consumption see the same object.
func JSONBudget(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tokens, entries, containers := 0, 0, 0
	var value func(int) error
	value = func(depth int) error {
		tokens++
		if depth > 32 || tokens > 1000000 {
			return errors.New("JSON complexity budget exceeded")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if s, ok := t.(string); ok && len(s) > 4096 {
			return errors.New("JSON string budget exceeded")
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		containers++
		if containers > 32768 {
			return errors.New("JSON container budget exceeded")
		}
		if delim != '[' && delim != '{' {
			return errors.New("unexpected JSON delimiter")
		}
		count := 0
		keys := map[string]bool{}
		for d.More() {
			count++
			if delim == '{' {
				if count > 128 {
					return errors.New("JSON object budget exceeded")
				}
				k, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := k.(string)
				if !ok || len(key) > 128 || keys[key] {
					return errors.New("invalid or duplicate JSON key")
				}
				keys[key] = true
			} else {
				entries++
				if count > 4096 || entries > 262144 {
					return errors.New("JSON array budget exceeded")
				}
			}
			if err := value(depth + 1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
