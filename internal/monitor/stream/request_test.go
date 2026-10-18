package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/monitor"
)

func request() Request {
	r := Request{Schema: 1, RequestID: "one", Source: "preview", Lang: "en", Interval: "1s", Template: monitor.Template{Schema: 1, Title: "Topophany", Rows: []monitor.Row{{ID: "status", Metric: "summary.verdict", Visual: "status"}}}}
	r.Template.Bar.Row = "status"
	return r
}
func line(r Request) Line { data, _ := json.Marshal(r); return Line{Data: data} }
func TestRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*Request)
		code   string
	}{
		{"valid", func(*Request) {}, ""},
		{"default client", func(r *Request) { r.Source = ""; r.Lang = "" }, ""},
		{"protocol", func(r *Request) { r.Schema = 2 }, "unsupported_protocol"},
		{"id bytes", func(r *Request) { r.RequestID = strings.Repeat("界", 22) }, "invalid_request"},
		{"source", func(r *Request) { r.Source = "collector" }, "invalid_request"},
		{"relative credentials", func(r *Request) { r.Source = "client"; r.Lang = ""; r.Credentials = "relative" }, "invalid_request"},
		{"profile", func(r *Request) { r.Profile = "bad/name" }, "invalid_request"},
		{"project", func(r *Request) { r.Project = "bad name" }, "invalid_request"},
		{"observer", func(r *Request) { r.Observer = "bad name" }, "invalid_request"},
		{"interval low", func(r *Request) { r.Interval = "999ms" }, "invalid_request"},
		{"interval high", func(r *Request) { r.Interval = "2h" }, "invalid_request"},
		{"interval bad", func(r *Request) { r.Interval = "hello" }, "invalid_request"},
		{"interval hour", func(r *Request) { r.Interval = "1h" }, ""},
		{"negative scenario", func(r *Request) { r.Scenario = -1 }, "invalid_request"},
		{"language", func(r *Request) { r.Lang = "de" }, "invalid_request"},
		{"client scenario", func(r *Request) { r.Source = "client"; r.Scenario = 1; r.Lang = "" }, "invalid_request"},
		{"client language", func(r *Request) { r.Source = "client" }, "invalid_request"},
		{"preview credentials", func(r *Request) { r.Credentials = filepath.Join(string(filepath.Separator), "tmp", "credentials.json") }, "invalid_request"},
		{"preview profile", func(r *Request) { r.Profile = "default" }, "invalid_request"},
		{"row visual", func(r *Request) { r.Template.Rows[0].Visual = "text" }, "invalid_request"},
		{"bar reference", func(r *Request) { r.Template.Bar.Row = "absent" }, "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request()
			tc.modify(&r)
			_, err := Decode(line(r))
			got := ""
			if err != nil {
				got = err.Code
			}
			if got != tc.code {
				t.Fatalf("got %+v, want %s", err, tc.code)
			}
		})
	}
	for _, raw := range []string{"", "null", "[]", "{} {}", "{\"schema\":\"1\"}", string([]byte{0xff})} {
		if _, err := Decode(Line{Data: []byte(raw)}); err == nil || err.Code != "invalid_request" {
			t.Fatal(raw, err)
		}
	}
}
func TestRequestLineBoundsAndRecovery(t *testing.T) {
	valid := line(request()).Data
	input := append(bytes.Repeat([]byte{'x'}, MaxRequestBytes+1), '\n')
	input = append(input, valid...)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := make(chan Line)
	go ReadLines(ctx, bytes.NewReader(input), lines)
	first := <-lines
	if _, err := Decode(first); err == nil || err.Code != "invalid_request" || !first.TooLong {
		t.Fatal(first, err)
	}
	if _, err := Decode(<-lines); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-lines; ok {
		t.Fatal("missing EOF")
	}
	atLimit := append(valid, bytes.Repeat([]byte{' '}, MaxRequestBytes-len(valid))...)
	lines = make(chan Line)
	go ReadLines(ctx, bytes.NewReader(append(atLimit, '\n')), lines)
	if _, err := Decode(<-lines); err != nil {
		t.Fatal("exact 256 KiB refused", err)
	}
	<-lines
}
func TestEverySourceErrorCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	creds := client.Credentials{Schema: 1, Active: "default", Profiles: map[string]client.Profile{"default": {Project: "lab", Servers: []client.Endpoint{{URL: "http://127.0.0.1:1", Token: strings.Repeat("a", 64)}}}}}
	if err := client.Save(path, creds); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		modify func(*Request)
		code   string
	}{
		{"credentials", func(r *Request) { r.Credentials = filepath.Join(t.TempDir(), "missing") }, "credentials_unavailable"},
		{"profile", func(r *Request) { r.Profile = "absent" }, "profile_not_found"},
		{"project", func(r *Request) { r.Project = "foreign" }, "project_mismatch"},
		{"scenario", func(r *Request) { r.Source = "preview"; r.Credentials = ""; r.Lang = "en"; r.Scenario = 999 }, "invalid_request"},
		{"observer", func(r *Request) { r.Source = "preview"; r.Credentials = ""; r.Lang = "en"; r.Observer = "absent" }, "invalid_request"},
		{"preview project", func(r *Request) { r.Source = "preview"; r.Credentials = ""; r.Lang = "en"; r.Project = "foreign" }, "project_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request()
			r.Source = "client"
			r.Lang = ""
			r.Credentials = path
			tc.modify(&r)
			source, err := OpenSource(context.Background(), r)
			if source.Close != nil {
				source.Close()
			}
			if err == nil || err.Code != tc.code {
				t.Fatal(err, tc.code)
			}
		})
	}
	r := request()
	r.Source = "client"
	r.Lang = ""
	r.Credentials = path
	r.Profile = ""
	source, e := OpenSource(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	source.Close()
}

type frameSink struct {
	bytes.Buffer
	writes, flushes int
	fail            error
}

func (s *frameSink) Write(p []byte) (int, error) {
	s.writes++
	if s.fail != nil {
		return 0, s.fail
	}
	return s.Buffer.Write(p)
}
func (s *frameSink) Flush() error { s.flushes++; return nil }
func TestFrameBoundWriteAndFlush(t *testing.T) {
	sink := &frameSink{}
	p := monitor.Panel{Schema: 1, Name: "stream", Rows: []monitor.Value{{Value: strings.Repeat("x", MaxFrameBytes)}}}
	if err := WriteFrame(sink, p); err != nil {
		t.Fatal(err)
	}
	if sink.Len() > MaxFrameBytes || sink.writes != 1 || sink.flushes != 1 {
		t.Fatal(sink.Len(), sink.writes, sink.flushes)
	}
	var got monitor.Panel
	if err := json.Unmarshal(bytes.TrimSpace(sink.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Rows[0].Value != nil || got.Rows[0].Reason != "frame_budget_exceeded" {
		t.Fatal(got)
	}
	if p.Rows[0].Value == nil {
		t.Fatal("mutated original frame")
	}
	sink = &frameSink{fail: io.ErrClosedPipe}
	if err := WriteFrame(sink, p); err != io.ErrClosedPipe || sink.flushes != 0 {
		t.Fatal(err, sink.flushes)
	}
}

func wait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("stream timed out")
		var zero T
		return zero
	}
}
