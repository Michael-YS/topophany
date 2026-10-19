package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestCompactDigestPreservesLegacyJSON(t *testing.T) {
	sum := sha256.Sum256([]byte("evidence"))
	hash := hex.EncodeToString(sum[:])
	at := time.Now().UTC()
	legacy := struct {
		Observer string    `json:"observer,omitempty"`
		Hash     string    `json:"hash"`
		At       time.Time `json:"at"`
	}{"home", hash, at}
	want, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(receipt{Observer: "home", Hash: sum, At: at})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("receipt schema changed: %s != %s", got, want)
	}
	var read receipt
	if err = json.Unmarshal(want, &read); err != nil || read.Hash != sum || !read.At.Equal(at) {
		t.Fatal("legacy receipt not readable", err)
	}
	escaped := strings.ReplaceAll(string(want), hash, "\\u"+fmt.Sprintf("%04x", hash[0])+hash[1:])
	if err = json.Unmarshal([]byte(escaped), &read); err != nil || read.Hash != sum {
		t.Fatal("escaped legacy digest not readable", err)
	}
	for _, raw := range []string{`null`, `""`, `"hash"`, `[]`, `"` + strings.Repeat("z", 64) + `"`} {
		if err = json.Unmarshal([]byte(raw), &read.Hash); err == nil {
			t.Fatal("invalid digest accepted", raw)
		}
	}
}

func TestStringSharingIsBoundedAndPreservesEvidence(t *testing.T) {
	var p stringPool
	a := p.intern(strings.Clone("repeated evidence"))
	b := p.intern(strings.Clone(a))
	if unsafe.StringData(a) != unsafe.StringData(b) {
		t.Fatal("equal text was not shared")
	}
	for i := 0; i < 5*stringGenerationSize; i++ {
		p.intern(fmt.Sprintf("different-%d", i))
	}
	if len(p.current) > stringGenerationSize || len(p.previous) > stringGenerationSize {
		t.Fatal("dictionary grew without bound")
	}
	large := strings.Repeat("x", maxSharedStringBytes+1)
	p.intern(large)
	if _, ok := p.current[large]; ok {
		t.Fatal("oversized arbitrary evidence retained by dictionary")
	}

	s := openTest(t, t.TempDir(), Options{})
	v := testkit.Observation(testkit.Config(), "home-http", 1, time.Now(), "ok")
	v.Detail = large
	v.Timings = map[string]int64{"connect": 12}
	original, _ := json.Marshal(v)
	compact := s.compactStrings(v)
	actual, _ := json.Marshal(compact)
	if !bytes.Equal(original, actual) {
		t.Fatal("sharing altered evidence")
	}
	compact.Facts["response_proof"] = "mutated"
	compact.Timings["connect"] = 99
	if v.Facts["response_proof"] == "mutated" || v.Timings["connect"] != 12 {
		t.Fatal("sharing borrowed mutable caller maps")
	}
}

func TestCheckpointBudgetCountsUTF8BytesOnBothWritePaths(t *testing.T) {
	var b bytes.Buffer
	w := checkpointJSON{w: &b, left: 6}
	if n, err := w.WriteString("你好"); err != nil || n != 6 {
		t.Fatal("UTF-8 byte accounting", n, err)
	}
	if _, err := w.Write([]byte("x")); err != ErrCapacity || b.String() != "你好" {
		t.Fatal("byte write exceeded budget", err)
	}
	b.Reset()
	w = checkpointJSON{w: &b, left: 6}
	if _, err := w.Write([]byte("你好")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("x"); err != ErrCapacity || b.String() != "你好" {
		t.Fatal("string write exceeded budget", err)
	}
}
