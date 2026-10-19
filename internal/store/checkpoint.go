package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Keep the existing checksum envelope and on-disk schema, but emit one record
// at a time. Whole-state json.Marshal also retained large pooled encode buffers
// and generated several simultaneous copies of the checkpoint.
func (s *Store) writeCheckpoint(path string) error {
	return secure.AtomicWriteStreamMeasured(path, 0600, &ioaccount.Checkpoint, func(f io.WriteSeeker) error {
		const prefix = `{"sha256":"`
		if _, err := io.WriteString(f, prefix+string(makeChecksumPlaceholder())+`","payload":`); err != nil {
			return err
		}
		h := sha256.New()
		w := bufio.NewWriterSize(io.MultiWriter(f, h), 64<<10)
		out := checkpointJSON{w: w, left: (256 << 20) - int64(len(prefix)+64+len(`","payload":`)+1)}
		out.text(`{"schema":1,"rows":[`)
		// Raw history is already unique. Only tails that outlive it need deduping.
		held := make(map[string]bool, len(s.rows))
		for i, v := range s.rows {
			if i > 0 {
				out.text(",")
			}
			out.value(v)
			held[v.ID] = true
		}
		first := len(s.rows) == 0
		keys := sortedKeys(s.tails)
		for _, key := range keys {
			for _, v := range s.tails[key] {
				if held[v.ID] {
					continue
				}
				if !first {
					out.text(",")
				}
				first = false
				out.value(v)
				held[v.ID] = true
			}
		}
		out.text(`],"seen":{`)
		for i, id := range sortedKeys(s.seen) {
			if i > 0 {
				out.text(",")
			}
			out.value(id)
			out.text(":")
			out.receipt(s.seen[id])
		}
		out.text(`},"beats":`)
		out.value(s.beats)
		out.text(`,"floors":`)
		out.value(s.floors)
		out.text(`,"floors_at":`)
		out.value(s.floorAt)
		out.text(`,"rollups":`)
		if out.err == nil {
			out.err = s.rollups.WriteJSON(&out)
		}
		out.text(`,"carries":`)
		out.value(s.carries)
		out.text("}")
		if out.err != nil {
			return out.err
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if _, err := io.WriteString(f, "}"); err != nil {
			return err
		}
		if _, err := f.Seek(int64(len(prefix)), io.SeekStart); err != nil {
			return err
		}
		_, err := io.WriteString(f, hex.EncodeToString(h.Sum(nil)))
		return err
	})
}

func makeChecksumPlaceholder() []byte {
	b := make([]byte, 64)
	for i := range b {
		b[i] = '0'
	}
	return b
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

type checkpointJSON struct {
	w    io.Writer
	left int64
	err  error
	enc  *json.Encoder
	hex  [64]byte
}

func (w *checkpointJSON) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if int64(len(p)) > w.left {
		w.err = ErrCapacity
		return 0, w.err
	}
	var n int
	n, w.err = w.w.Write(p)
	if w.err == nil && n != len(p) {
		w.err = io.ErrShortWrite
	}
	w.left -= int64(n)
	return n, w.err
}

func (w *checkpointJSON) WriteString(s string) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if int64(len(s)) > w.left {
		w.err = ErrCapacity
		return 0, w.err
	}
	n, err := io.WriteString(w.w, s)
	w.left -= int64(n)
	if err == nil && n != len(s) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}
func (w *checkpointJSON) text(s string) {
	if w.err == nil {
		_, w.err = io.WriteString(w, s)
	}
}
func (w *checkpointJSON) value(v any) {
	if w.err != nil {
		return
	}
	if w.enc == nil {
		w.enc = json.NewEncoder(w)
	}
	w.err = w.enc.Encode(v)
}

// Avoid allocating and validating a custom JSON marshaler buffer for every
// receipt while retaining the legacy hexadecimal string representation.
func (w *checkpointJSON) receipt(r receipt) {
	w.text("{")
	if r.Observer != "" {
		w.text(`"observer":`)
		w.value(r.Observer)
		w.text(",")
	}
	w.text(`"hash":"`)
	if w.err == nil {
		hex.Encode(w.hex[:], r.Hash[:])
		_, w.err = w.Write(w.hex[:])
	}
	w.text(`","at":`)
	w.value(r.At)
	w.text("}")
}
