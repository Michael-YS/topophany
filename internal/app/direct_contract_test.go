package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/monitor/stream"
	"github.com/WhitenWhiten/topophany/internal/report"
)

// Canonical fixtures use production serializers with fixed clocks and opaque
// synthetic identifiers. Adversarial extension fixtures are explicitly derived
// from those same bytes. Updating them is an explicit authoring operation.
func TestDirectContractFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "contracts", "direct-v1")
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	src := direct.SourceConfig{ID: "bs-00000000000000000000000000000001", Name: "Synthetic Beszel", Adapter: "beszel", URL: "http://127.0.0.1:8099", Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}
	object := "aaaaaaaaaaaaaaa"
	nodes := []direct.Node{{ID: src.ID + "." + object, ObjectID: object, Name: "Synthetic server"}}
	entry := direct.Entry{Source: src, AuthMode: "password", Selected: []string{object}, Nodes: nodes, Credential: "synthetic-reference", Subject: "users:uuuuuuuuuuuuuuu"}
	profile := direct.Profile{Profile: "desktop", Project: "direct-synthetic", Revision: "revision-synthetic-1", Dataset: "dataset-synthetic-1", Sources: []direct.Entry{entry}}
	template := monitor.Template{Schema: 1, Title: "Topophany", Rows: []monitor.Row{{ID: "status", Label: "", Metric: "sources.status", Visual: "status"}, {ID: "servers", Label: "", Metric: "servers.list", Visual: "list"}, {ID: "server", Label: "", Metric: "server.card", Select: monitor.Selector{Node: nodes[0].ID}, Visual: "card"}}}
	template.Bar.Row = "status"
	request := stream.Request{Schema: 1, RequestID: "settings-42", Source: "direct", Profile: profile.Profile, ProfileRevision: profile.Revision, Interval: "5s", Template: template}
	files := map[string][]byte{}
	line := func(name string, value any) {
		t.Helper()
		var b bytes.Buffer
		if e := writeJSONLine(&b, value); e != nil {
			t.Fatal(e)
		}
		files["fixtures/"+name+".json"] = b.Bytes()
	}
	var caps bytes.Buffer
	if e := writeCapabilities(&caps); e != nil {
		t.Fatal(e)
	}
	files["fixtures/capabilities.json"] = caps.Bytes()
	line("request-direct", request)
	success := func(name string, data any) {
		line(name, sourceEnvelope{Schema: 1, RequestID: "settings-42", OK: true, Data: data})
	}
	publicEntry := map[string]any{"source": src, "auth_mode": "password", "selected_objects": entry.Selected, "nodes": nodes}
	success("sources-list-success", map[string]any{"profiles": []any{map[string]any{"profile": profile.Profile, "project": profile.Project, "profile_revision": profile.Revision, "dataset_id": profile.Dataset, "sources": []any{publicEntry}}}})
	// list has no request ID input.
	var list map[string]any
	_ = json.Unmarshal(files["fixtures/sources-list-success.json"], &list)
	list["request_id"] = ""
	line("sources-list-success", list)
	success("sources-inspect-success", map[string]any{"source": src, "systems": []direct.System{{ID: object, Name: nodes[0].Name}}, "truncated": false, "limits": map[string]int{"max_selected": 20}, "warnings": []string{}})
	success("sources-inspect-truncated", map[string]any{"source": src, "systems": []direct.System{{ID: object, Name: nodes[0].Name}}, "truncated": true, "limits": map[string]int{"max_selected": 20}, "warnings": []string{}})
	success("sources-save-success", map[string]any{"profile": profile.Profile, "profile_revision": profile.Revision, "dataset_id": profile.Dataset, "source_id": src.ID, "nodes": nodes})
	success("sources-remove-success", map[string]any{"profile": profile.Profile, "profile_revision": "revision-synthetic-2", "dataset_id": "dataset-synthetic-empty", "source_id": src.ID})
	for name, code := range map[string]string{"sources-list-error": "storage_error", "sources-inspect-error": "auth_required", "sources-save-error": "profile_conflict", "sources-remove-error": "source_not_found", "sources-inspect-forbidden": "forbidden", "sources-inspect-mfa": "mfa_required", "sources-invalid-request": "invalid_request"} {
		id := "settings-42"
		if name == "sources-list-error" {
			id = ""
		}
		line(name, sourceEnvelope{Schema: 1, RequestID: id, Error: direct.AsError(&direct.Error{Code: code, Message: map[string]string{"storage_error": "Direct profile storage is unavailable.", "auth_required": "Authentication is required; the Hub did not accept this credential.", "profile_conflict": "The profile has changed. List profiles before retrying.", "source_not_found": "Direct source not found.", "forbidden": "The Hub denied permission for this operation.", "mfa_required": "The Hub requires an additional authentication step; use a token.", "invalid_request": "Invalid direct request."}[code]})})
	}
	makeFrame := func(name string, p direct.Profile, state string, sampleAt time.Time, include bool) {
		t.Helper()
		c, e := p.Config()
		if e != nil {
			t.Fatal(e)
		}
		snap := direct.LoadingSnapshot(p, "", now)
		ext := model.NewExternalState()
		if state != "loading" {
			for _, s := range c.Sources {
				ext.Sources[s.ID] = report.MergeSource(model.SourceState{}, s.ID, now, true, "", 0)
			}
		}
		if include {
			cpu := 25.0
			total, used := uint64(9007199254740993), uint64(4503599627370496)
			r := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: 1, Object: object, SampleID: "sssssssssssssss", ObservedAt: sampleAt, TimeBasis: model.TimeBasisUpstreamSample}, Resource: &model.ResourceReport{CPU: &model.CPUReport{Percent: &cpu}, Memory: &model.MemoryReport{TotalBytes: &total, UsedBytes: &used}, Disks: []model.DiskReport{{ID: "root", Percent: &cpu}}}}
			x := c.Checks[0]
			binding, _ := report.MergeBinding(x.ID, config.Contract(c, x), *x.External, model.BindingState{}, r, now)
			ext.Bindings[x.ID] = binding
		}
		info := snap.Direct
		snap = engine.NewPlan(c).PrepareExternal(nil, nil, nil, ext).Build("fixture-session", "", now)
		snap.Direct = info
		until := now.Add(180 * time.Second)
		for i := range info.Sources {
			s := &info.Sources[i]
			s.State = "connected"
			s.LastAttemptAt = &now
			s.LastContactAt = &now
			s.ContactValidUntil = &until
			if state == "loading" {
				s.State = "loading"
				s.LastAttemptAt = nil
				s.LastContactAt = nil
				s.ContactValidUntil = nil
			}
			if state == "auth_required" || state == "forbidden" || state == "retrying" {
				s.State = state
				code := state
				if state == "retrying" {
					code = "connection_failed"
					next := now.Add(5 * time.Second)
					s.NextRetryAt = &next
				}
				s.Code = &code
				s.LastContactAt = nil
				s.ContactValidUntil = nil
			}
		}
		if name == "stream-partial" {
			s := &info.Sources[1]
			s.State = "forbidden"
			code := "forbidden"
			s.Code = &code
			s.LastContactAt = nil
			s.ContactValidUntil = nil
		}
		pconf := monitor.Config{Schema: 1, Name: "stream", Revision: "stream-config-1", Source: "direct", Profile: p.Profile, Interval: "5s", Template: template}
		frame := monitor.Build(pconf, monitor.Input{Snapshot: snap}, now)
		frame.RequestID = "settings-42"
		frame.Instance = "fixture-stream"
		frame.Sequence = 1
		frame.PublisherExpiresAt = now.Add(30 * time.Second)
		var b bytes.Buffer
		if e = stream.WriteFrame(&b, frame); e != nil {
			t.Fatal(e)
		}
		files["fixtures/"+name+".json"] = b.Bytes()
	}
	makeFrame("stream-loading", profile, "loading", now, false)
	makeFrame("stream-ready", profile, "connected", now.Add(-time.Minute), true)
	makeFrame("stream-no-data", profile, "connected", now, false)
	makeFrame("stream-auth-required", profile, "auth_required", now, false)
	makeFrame("stream-forbidden", profile, "forbidden", now, false)
	makeFrame("stream-retrying", profile, "retrying", now, false)
	makeFrame("stream-expired", profile, "connected", now.Add(-181*time.Second), true)
	makeFrame("stream-reconnected", profile, "connected", now, true)
	other := entry
	other.Source.ID = "bs-00000000000000000000000000000002"
	other.Nodes = []direct.Node{{ID: other.Source.ID + "." + object, ObjectID: object, Name: "Other synthetic server"}}
	partial := profile
	partial.Sources = append(append([]direct.Entry(nil), profile.Sources...), other)
	makeFrame("stream-partial", partial, "connected", now, true)
	switched := profile
	switched.Profile = "alternate"
	switched.Revision = "revision-synthetic-alternate"
	switched.Dataset = "dataset-synthetic-alternate"
	makeFrame("stream-config-switch", switched, "connected", now, true)
	for _, name := range []string{"stream-unknown-fields", "stream-unknown-adapter", "stream-malformed-origin"} {
		var frame map[string]any
		decoder := json.NewDecoder(bytes.NewReader(files["fixtures/stream-ready.json"]))
		decoder.UseNumber()
		_ = decoder.Decode(&frame)
		rows := frame["rows"].([]any)
		switch name {
		case "stream-unknown-fields":
			frame["future_extension"] = map[string]any{"enabled": true}
			rows[0].(map[string]any)["future_row_field"] = "ignored"
		case "stream-unknown-adapter":
			rows[0].(map[string]any)["value"].(map[string]any)["sources"].([]any)[0].(map[string]any)["adapter"] = "future-adapter"
			rows[1].(map[string]any)["value"].([]any)[0].(map[string]any)["origin"].(map[string]any)["adapter"] = "future-adapter"
		case "stream-malformed-origin":
			rows[1].(map[string]any)["value"].([]any)[0].(map[string]any)["origin"] = map[string]any{"adapter": 17, "source_id": []any{}, "name": nil}
			card := rows[2].(map[string]any)["value"].(map[string]any)
			card["origin"] = "malformed"
			card["resources"].([]any)[0].(map[string]any)["origin"] = false
		}
		line(name, frame)
	}
	lines := make(chan stream.Line, 1)
	rejected := request
	rejected.RequestID = "settings-43"
	raw, _ := json.Marshal(rejected)
	lines <- stream.Line{Data: raw}
	close(lines)
	frames := []monitor.Panel{}
	err := stream.RunClock(context.Background(), lines, func(context.Context, stream.Request) (stream.Source, *stream.Error) {
		return stream.Source{}, &stream.Error{Code: "profile_conflict", Message: "Direct profile changed; list profiles before retrying."}
	}, func(p monitor.Panel) error { frames = append(frames, p); return nil }, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	line("stream-request-error", frames[0])
	type item struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	manifest := struct {
		ContractID string `json:"contract_id"`
		Schema     int    `json:"fixture_schema"`
		Files      []item `json:"files"`
	}{"topophany-direct-v1", 1, []item{}}
	paths := []string{}
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	update := os.Getenv("TOPO_UPDATE_DIRECT_CONTRACT") == "1"
	for _, path := range paths {
		data := files[path]
		if !json.Valid(bytes.TrimSpace(data)) {
			t.Fatal(path)
		}
		if update {
			if e := os.WriteFile(filepath.Join(root, path), data, 0644); e != nil {
				t.Fatal(e)
			}
		}
		expected, e := os.ReadFile(filepath.Join(root, path))
		if e != nil || !bytes.Equal(data, expected) {
			t.Fatalf("fixture mismatch %s: %v", path, e)
		}
		h := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, item{path, hex.EncodeToString(h[:])})
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	encoded = append(encoded, '\n')
	if update {
		if e := os.WriteFile(filepath.Join(root, "manifest.json"), encoded, 0644); e != nil {
			t.Fatal(e)
		}
	}
	stored, e := os.ReadFile(filepath.Join(root, "manifest.json"))
	if e != nil || !bytes.Equal(stored, encoded) {
		t.Fatal("contract manifest mismatch", e)
	}
}
