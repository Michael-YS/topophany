package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func TestOutputLockDedupLeaseAndStop(t *testing.T) {
	c := testConfig()
	c.Revision = "test"
	c.Output = filepath.Join(t.TempDir(), "metrics.json")
	out, e := OpenOutput(c.Output, c.Name)
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()
	if other, e := OpenOutput(c.Output, c.Name); e == nil {
		other.Close()
		t.Fatal("second writer admitted")
	}
	now := time.Now().UTC()
	p := Build(c, hostInput(now), now)
	var syncs atomic.Int64
	stop := secure.WatchSyncs(func(string) { syncs.Add(1) })
	defer stop()
	if e = out.Write(p, time.Second, false); e != nil {
		t.Fatal(e)
	}
	first, _ := os.ReadFile(c.Output)
	p.GeneratedAt = now.Add(2 * time.Second)
	if e = out.Write(p, time.Second, false); e != nil {
		t.Fatal(e)
	}
	second, _ := os.ReadFile(c.Output)
	if string(first) != string(second) {
		t.Fatal("unchanged evidence rewritten")
	}
	p.GeneratedAt = now.Add(11 * time.Second)
	out.Write(p, time.Second, false)
	second, _ = os.ReadFile(c.Output)
	if string(first) == string(second) {
		t.Fatal("publisher lease not renewed")
	}
	p.Lifecycle = "stopped"
	out.Write(p, time.Second, true)
	b, _ := os.ReadFile(c.Output)
	var got Panel
	json.Unmarshal(b, &got)
	if got.Lifecycle != "stopped" || !got.PublisherExpiresAt.Equal(got.GeneratedAt) {
		t.Fatal(got)
	}
	if syncs.Load() != 0 {
		t.Fatal("cache forced directory sync")
	}
}
func TestOutputProtectsNonPanelAndAliases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "other.json")
	os.WriteFile(path, []byte(`{"secret":"fixture"}`), 0600)
	if _, e := PrepareOutput(path, "desktop"); e == nil {
		t.Fatal("overwrites unrelated file")
	}
	if _, e := PrepareOutput(filepath.Join(dir, "metrics.json"), "desktop", dir); e == nil {
		t.Fatal("writes protected data directory")
	}
}
func TestHubSnapshotIsolationAndSlowSubscriber(t *testing.T) {
	h := &Hub{}
	p := Panel{Rows: []Value{{Text: "original"}}}
	h.Publish(p)
	p.Rows[0].Text = "mutated"
	ch, stop, e := h.Subscribe()
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	if v := <-ch; v.Rows[0].Text != "original" {
		t.Fatal(v)
	}
	for i := uint64(0); i < 100; i++ {
		p.Sequence = i
		h.Publish(p)
	}
	if v := <-ch; v.Sequence != 99 {
		t.Fatal(v)
	}
	v, _ := h.Latest()
	v.Rows[0].Text = "other"
	v, _ = h.Latest()
	if v.Rows[0].Text == "other" {
		t.Fatal("shared snapshot")
	}
}
func TestRuntimeShutdownWritesStopped(t *testing.T) {
	c := testConfig()
	c.Revision = "test"
	c.Output = filepath.Join(t.TempDir(), "metrics.json")
	out, e := OpenOutput(c.Output, c.Name)
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{}
	frames, unsub, _ := h.Subscribe()
	defer unsub()
	done := make(chan struct{})
	go func() {
		Run(ctx, c, func(context.Context) (Input, error) { return hostInput(time.Now()), nil }, out, h, nil)
		close(done)
	}()
	select {
	case <-frames:
		cancel()
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("no immediate snapshot")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown blocked")
	}
	b, e := os.ReadFile(c.Output)
	if e != nil {
		t.Fatal(e)
	}
	var p Panel
	json.Unmarshal(b, &p)
	if p.Lifecycle != "stopped" {
		t.Fatal(p)
	}
	latest, _ := h.Latest()
	if latest.Lifecycle != "stopped" || latest.Rows[0].Value != nil {
		t.Fatal("in-memory subscriber missed stopped state", latest)
	}
}

func TestWriteFailurePreservesSnapshotAndCanRecover(t *testing.T) {
	c := testConfig()
	c.Revision = "fixture"
	dir := t.TempDir()
	c.Output = filepath.Join(dir, "metrics.json")
	out, e := OpenOutput(c.Output, c.Name)
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()
	now := time.Now().UTC()
	p := Build(c, hostInput(now), now)
	if e = out.Write(p, time.Second, true); e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(c.Output)
	// A directory replacing the destination is a deterministic write failure,
	// including when tests run as root; no chmod-based fault assumption.
	backup := filepath.Join(dir, "previous.json")
	if e = os.Rename(c.Output, backup); e != nil {
		t.Fatal(e)
	}
	os.Mkdir(c.Output, 0700)
	p.GeneratedAt = now.Add(time.Minute)
	if e = out.Write(p, time.Second, true); e == nil {
		t.Fatal("write failure ignored")
	}
	b, _ := os.ReadFile(backup)
	if string(b) != string(original) {
		t.Fatal("last complete snapshot changed")
	}
	os.Remove(c.Output)
	os.Rename(backup, c.Output)
	if e = out.Write(p, time.Second, true); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(c.Output)
	if string(b) == string(original) {
		t.Fatal("did not recover")
	}
}

func TestRuntimeWithoutFileOutput(t *testing.T) {
	h := &Hub{}
	frames, unsubscribe, e := h.Subscribe()
	if e != nil {
		t.Fatal(e)
	}
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, testConfig(), func(context.Context) (Input, error) { return hostInput(time.Now()), nil }, nil, h, nil)
		close(done)
	}()
	select {
	case <-frames:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("no memory snapshot")
	}
	<-done
	p, _ := h.Latest()
	if p.Lifecycle != "stopped" {
		t.Fatal(p)
	}
}

func TestReaderFutureClockBounds(t *testing.T) {
	now := time.Now().UTC()
	c := testConfig()
	p := Build(c, hostInput(now), now)
	p.PublisherExpiresAt = now.Add(30 * time.Second)
	// A modest reader clock offset is permitted by the five-minute reader rule.
	p.GeneratedAt = now.Add(time.Minute)
	if got := Current(p, now); got.Lifecycle != "running" {
		t.Fatal(got)
	}
	p.GeneratedAt = now.Add(6 * time.Minute)
	if got := Current(p, now); got.Lifecycle != "publisher_expired" {
		t.Fatal(got)
	}
	p.GeneratedAt = now
	p.Rows[0].SampledAt = now.Add(6 * time.Minute)
	if got := Current(p, now).Rows[0]; got.Value != nil || got.Reason != "clock_skew" {
		t.Fatal(got)
	}
}

func TestHubOwnsNestedEvidence(t *testing.T) {
	n := float64(42)
	p := Panel{Rows: []Value{{Value: ServerCard{Resources: []ResourceValue{{}}}}}}
	card := p.Rows[0].Value.(ServerCard)
	card.Resources[0].CPU.Percent = &n
	p.Rows[0].Value = card
	h := &Hub{}
	h.Publish(p)
	n = 99
	first, _ := h.Latest()
	c := first.Rows[0].Value.(ServerCard)
	if *c.Resources[0].CPU.Percent != 42 {
		t.Fatal("publisher shared nested value")
	}
	*c.Resources[0].CPU.Percent = 18
	second, _ := h.Latest()
	if *second.Rows[0].Value.(ServerCard).Resources[0].CPU.Percent != 42 {
		t.Fatal("subscriber shared nested value")
	}
}

func TestNestedHealthTransitionsBypassCadence(t *testing.T) {
	now := time.Now().UTC()
	last := Panel{Lifecycle: "running", GeneratedAt: now, Rows: []Value{{Health: "up", Freshness: "fresh", Value: ServerCard{Health: "up", Resources: []ResourceValue{{Check: "host", Health: "up", Freshness: "fresh"}}}}}}
	next := clone(last)
	next.GeneratedAt = now.Add(time.Second)
	card := next.Rows[0].Value.(ServerCard)
	card.Resources[0].Freshness = "stale"
	next.Rows[0].Value = card
	if !ShouldPublish(last, ContentDigest(last), next, time.Hour) {
		t.Fatal("nested stale resource waited for display cadence")
	}
	next = clone(last)
	next.GeneratedAt = now.Add(time.Second)
	next.Rows[0].Text = "a changed reading"
	if ShouldPublish(last, ContentDigest(last), next, time.Hour) {
		t.Fatal("ordinary value bypassed cadence")
	}
}
