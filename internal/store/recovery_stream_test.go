package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestStreamedRecoveryPreservesLegacyOrderingAndWhitespace(t *testing.T) {
	c := testkit.Config()
	now := time.Now().UTC()
	v := testkit.Observation(c, "home-http", 1, now, "ok")
	v.ReceivedAt = now
	state := diskState{Schema: 1, Rows: []model.Observation{v}, Seen: map[string]receipt{v.ID: {Hash: observationHash(v), At: now, Observer: v.Observer}}, Beats: map[string]model.Heartbeat{}}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{string(raw), pretty.String()} {
		// Both legacy encoders and hand-formatted/reordered envelopes are valid.
		for _, envelope := range []string{
			`{"sha256":"` + hashBytes([]byte(payload)) + `","payload": ` + payload + ` }`,
			`{"future":{"nested":[true,{"x":null}]},"payload": ` + payload + `,"sha256":"` + hashBytes([]byte(payload)) + `"}`,
		} {
			dir := t.TempDir()
			path := filepath.Join(dir, "checkpoint.json")
			if err = os.WriteFile(path, []byte(envelope), 0600); err != nil {
				t.Fatal(err)
			}
			s := openTest(t, dir, Options{Now: func() time.Time { return now }})
			history, _ := s.View()
			if len(history) != 1 || history[0].ID != v.ID || len(s.seen) != 1 {
				t.Fatal("legacy recovery lost history/receipts")
			}
			if err = s.Put(batch(c, v)); err != nil || len(s.seen) != 1 {
				t.Fatal("restored duplicate reapplied", err)
			}
		}
	}
}

func TestStreamedRecoveryRefusesCorruptionBeforeJournalOpen(t *testing.T) {
	valid := `{"schema":1,"rows":[],"seen":{},"beats":{}}`
	cases := []string{
		`{"sha256":"wrong","payload":` + valid + `}`,
		`{"payload":` + valid + `}`,
		`{"sha256":"` + hashBytes([]byte(valid)) + `","payload":` + valid + `} {}`,
	}
	for _, payload := range []string{
		`{"schema":2,"seen":{},"beats":{}}`,
		`{"schema":1,"seen":null,"beats":{}}`,
		`{"schema":1,"seen":{},"beats":null}`,
		`{"schema":1,"seen":{},"beats":{},"rows":{}}`,
		`{"schema":1,"seen":{},"beats":{},"rows":[]`,
	} {
		cases = append(cases, `{"sha256":"`+hashBytes([]byte(payload))+`","payload":`+payload+`}`)
	}
	for _, raw := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "checkpoint.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if s, err := Open(dir, Options{}); err == nil {
			s.Close()
			t.Fatal("invalid checkpoint accepted", raw)
		}
		if _, err := os.Stat(filepath.Join(dir, "journal.jsonl")); !os.IsNotExist(err) {
			t.Fatal("journal opened before checkpoint verified")
		}
		got, _ := os.ReadFile(path)
		if string(got) != raw {
			t.Fatal("corrupt checkpoint overwritten")
		}
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "checkpoint.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(dir, Options{}); err == nil {
		s.Close()
		t.Fatal("non-regular checkpoint accepted")
	}
}

func TestStreamedRecoveryCarriesPrecedeTailRebuilding(t *testing.T) {
	c, _ := flappingConfig()
	now := time.Now().UTC()
	opts := Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), MaxHistory: 10, Now: func() time.Time { return now }}
	dir := t.TempDir()
	s := openTest(t, dir, opts)
	rows := flapping(c, "home-http", 181, now.Add(-30*time.Minute), 10*time.Second)
	for _, v := range rows {
		if err := s.Put(batch(c, v)); err != nil {
			t.Fatal(err)
		}
	}
	want := judged(t, s, c, "home-http", now)
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	raw, _ := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	payload, err := unwrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	// Include unknown state fields, and put rows after carries. Recovery must
	// give the same hysteresis regardless of where fields occur in the file.
	order := []string{"schema", "seen", "beats", "carries", "rows", "rollups", "floors", "floors_at"}
	parts := []string{`"future":[{"nested":[1,2,3]}]`}
	for _, key := range order {
		if val, ok := fields[key]; ok {
			parts = append(parts, `"`+key+`":`+string(val))
		}
	}
	changed := []byte("{" + strings.Join(parts, ",") + "}")
	envelope := `{"sha256":"` + hashBytes(changed) + `","payload":` + string(changed) + `}`
	if err = os.WriteFile(filepath.Join(dir, "checkpoint.json"), []byte(envelope), 0600); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir, opts)
	got := judged(t, s, c, "home-http", now)
	if got.Health != want.Health || got.Reason != want.Reason || !got.Since.Equal(want.Since) {
		t.Fatal("streaming changed historical failure confirmation", want, got)
	}
}
