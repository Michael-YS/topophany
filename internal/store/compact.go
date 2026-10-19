package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Keep SHA-256 as its 32 bytes instead of retaining a separately allocated
// 64-byte hexadecimal string per receipt and retained row. JSON stays unchanged.
type digest [sha256.Size]byte

func (h digest) MarshalJSON() ([]byte, error) {
	b := make([]byte, 2+hex.EncodedLen(len(h)))
	b[0], b[len(b)-1] = '"', '"'
	hex.Encode(b[1:len(b)-1], h[:])
	return b, nil
}

func (h *digest) UnmarshalJSON(raw []byte) error {
	// The common on-disk form needs no temporary decoded string. Legal JSON
	// escapes remain readable for compatibility with externally rewritten files.
	if len(raw) == 66 && raw[0] == '"' && raw[65] == '"' {
		_, err := hex.Decode(h[:], raw[1:65])
		return err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	if len(s) != 64 {
		return errors.New("invalid SHA-256 digest")
	}
	_, err := hex.Decode(h[:], []byte(s))
	return err
}

const (
	stringGenerationSize = 4096
	maxSharedStringBytes = 256
)

// A bounded two-generation dictionary shares immutable text between retained
// rows. Generations rotate so one-off values are released after the workload
// changes; neither arbitrary diagnostics nor changing worker boots grow it
// without limit. Both maps are accessed only under the store lock.
type stringPool struct{ current, previous map[string]string }

func (p *stringPool) intern(s string) string {
	if len(s) == 0 || len(s) > maxSharedStringBytes {
		return s
	}
	if found, ok := p.current[s]; ok {
		return found
	}
	if found, ok := p.previous[s]; ok {
		s = found
	}
	if p.current == nil || len(p.current) >= stringGenerationSize {
		p.previous = p.current
		p.current = make(map[string]string)
	}
	p.current[s] = s
	return s
}

func (s *Store) compactStrings(v model.Observation) model.Observation {
	p := &s.strings
	v.Project = p.intern(v.Project)
	v.Contract = p.intern(v.Contract)
	v.Revision = p.intern(v.Revision)
	v.Check = p.intern(v.Check)
	v.Observer = p.intern(v.Observer)
	v.Boot = p.intern(v.Boot)
	v.Outcome = p.intern(v.Outcome)
	v.Evidence = p.intern(v.Evidence)
	v.Stage = p.intern(v.Stage)
	v.Code = p.intern(v.Code)
	v.Detail = p.intern(v.Detail)
	v.PathEvidence = p.intern(v.PathEvidence)
	if v.Facts != nil {
		facts := make(map[string]string, len(v.Facts))
		for k, val := range v.Facts {
			facts[p.intern(k)] = p.intern(val)
		}
		v.Facts = facts
	}
	if v.Timings != nil {
		timings := make(map[string]int64, len(v.Timings))
		for k, val := range v.Timings {
			timings[p.intern(k)] = val
		}
		v.Timings = timings
	}
	return v
}
