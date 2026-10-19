package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
)

const maxCheckpointBytes = 256 << 20

// Decode rows and receipts incrementally into the live store, rather than
// keeping the complete file plus a second decoded history in memory. Verify
// the original payload bytes through ReadAt before any journal is opened.
// Atomic checkpoint replacement leaves this open file descriptor stable.
func (s *Store) readCheckpoint(path string) error {
	// Reject FIFOs/devices before opening, as ReadBounded does. Recheck the
	// opened file too, since the path can change between stat and open.
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCheckpointBytes {
		return errors.New("checkpoint limit exceeded or non-regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCheckpointBytes {
		return errors.New("checkpoint limit exceeded or non-regular file")
	}
	source := &io.LimitedReader{R: f, N: maxCheckpointBytes + 1}
	d := json.NewDecoder(source)
	d.UseNumber()
	if err = expectDelimiter(d, '{'); err != nil {
		return err
	}
	fields := map[string]bool{}
	var expected string
	var start, end int64
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return e
		}
		name, ok := key.(string)
		if !ok || fields[name] || len(fields) >= 128 {
			return errors.New("invalid or duplicate checkpoint field")
		}
		fields[name] = true
		switch name {
		case "sha256":
			err = d.Decode(&expected)
		case "payload":
			if err = expectDelimiter(d, '{'); err != nil {
				return err
			}
			start = d.InputOffset() - 1
			err = s.readCheckpointState(d)
			end = d.InputOffset()
		default:
			err = skipJSONValue(d, 0)
		}
		if err != nil {
			return fmt.Errorf("invalid checkpoint: %w", err)
		}
	}
	if err = expectDelimiter(d, '}'); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return errors.New("checkpoint must contain one JSON value")
	}
	if source.N == 0 {
		return errors.New("checkpoint limit exceeded")
	}
	if !fields["sha256"] || !fields["payload"] {
		return errors.New("checkpoint checksum/payload required")
	}
	h := sha256.New()
	if _, err = io.Copy(h, io.NewSectionReader(f, start, end-start)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("checkpoint corruption: checksum mismatch")
	}
	// Carries must be installed before tails are trimmed: the failure that
	// started an incident may predate all of the raw history still retained.
	s.queueFloors()
	for _, v := range s.rows {
		s.indexRow(v)
		if _, ok := s.seen[v.ID]; !ok && !s.expired(v.ID) {
			s.seen[v.ID] = receipt{Hash: observationHash(v), At: v.ReceivedAt, Observer: v.Observer}
		}
	}
	return nil
}

func (s *Store) readCheckpointState(d *json.Decoder) error {
	fields := map[string]bool{}
	schema := 0
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || fields[name] || len(fields) >= 128 {
			return errors.New("invalid or duplicate state field")
		}
		fields[name] = true
		switch name {
		case "schema":
			err = d.Decode(&schema)
		case "rows":
			err = s.readCheckpointRows(d)
		case "seen":
			err = s.readCheckpointReceipts(d)
		case "beats":
			err = d.Decode(&s.beats)
		case "floors":
			var m map[string]uint64
			if err = d.Decode(&m); err == nil && m != nil {
				s.floors = m
			}
		case "floors_at":
			err = decodeOptionalMap(d, &s.floorAt)
		case "carries":
			var m map[string]engine.Carry
			if err = d.Decode(&m); err == nil && m != nil {
				s.carries = m
			}
		case "rollups":
			var raw borrowedJSON
			if err = d.Decode(&raw); err == nil && !bytes.Equal(raw, []byte("null")) {
				err = s.rollups.UnmarshalJSON(raw)
			}
		default:
			err = skipJSONValue(d, 0)
		}
		if err != nil {
			return err
		}
	}
	if err := expectDelimiter(d, '}'); err != nil {
		return err
	}
	if schema != 1 || !fields["seen"] || !fields["beats"] || s.beats == nil {
		return errors.New("invalid checkpoint state")
	}
	return nil
}

func (s *Store) readCheckpointRows(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return nil
	}
	if token != json.Delim('[') {
		return errors.New("checkpoint rows must be an array")
	}
	// A small checkpoint must not reserve a full 20,000-row history on
	// restart. Grow as records arrive, then discard excess backing capacity.
	s.rows = make([]model.Observation, 0, min(s.options.MaxHistory, 256))
	for d.More() {
		var v model.Observation
		if err = d.Decode(&v); err != nil {
			return err
		}
		s.rows = append(s.rows, s.compactStrings(v))
	}
	if err = expectDelimiter(d, ']'); err != nil {
		return err
	}
	if cap(s.rows) > len(s.rows)+len(s.rows)/8 {
		rows := make([]model.Observation, len(s.rows))
		copy(rows, s.rows)
		s.rows = rows
	}
	return nil
}

func (s *Store) readCheckpointReceipts(d *json.Decoder) error {
	if err := expectDelimiter(d, '{'); err != nil {
		return err
	}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return err
		}
		id, ok := token.(string)
		if !ok {
			return errors.New("invalid checkpoint receipt ID")
		}
		var r receipt
		if err = d.Decode(&r); err != nil {
			return err
		}
		r.Observer = s.strings.intern(r.Observer)
		s.seen[id] = r
	}
	return expectDelimiter(d, '}')
}

func expectDelimiter(d *json.Decoder, want byte) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token != json.Delim(want) {
		return errors.New("unexpected checkpoint JSON delimiter")
	}
	return nil
}

func decodeOptionalMap[V any](d *json.Decoder, keep *map[string]V) error {
	var m map[string]V
	if err := d.Decode(&m); err != nil {
		return err
	}
	if m != nil {
		*keep = m
	}
	return nil
}

// Unknown fields remain forward compatible without materializing their entire
// object/array values. The current schema never needs deep nesting.
func skipJSONValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("checkpoint JSON nesting limit exceeded")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter != '[' && delimiter != '{' {
		return errors.New("invalid checkpoint JSON")
	}
	for d.More() {
		if delimiter == '{' {
			if _, err = d.Token(); err != nil {
				return err
			}
		}
		if err = skipJSONValue(d, depth+1); err != nil {
			return err
		}
	}
	close := byte(']')
	if delimiter == '{' {
		close = '}'
	}
	return expectDelimiter(d, close)
}
