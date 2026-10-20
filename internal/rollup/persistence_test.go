package rollup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Each of the first-byte lists is read by the histogram decoder, in every
// class, and the decoder is what refuses a list the store could not have
// written. A document with the bad list in one class is refused, while the
// same document with that class empty is read, so the refusal is the
// list's and not the document's.
func TestFirstByteListsReachTheHistogramDecoder(t *testing.T) {
	control := fbDoc(`[[],[],[]]`)
	if e := json.Unmarshal([]byte(control), New(MaxLimit)); e != nil {
		t.Fatal("the control document is refused:", e)
	}
	for _, tc := range []struct {
		name string
		list []int64
	}{
		{"too short to hold a bin", []int64{1, 0, 0}},
		{"a count that does not add up", []int64{2, 10, 50, 20, 1}},
		{"a bin without its count", []int64{1, 0, 0, 20, 1, 30}},
		{"bins out of order", []int64{2, 10, 50, 30, 1, 20, 1}},
		{"bins repeated", []int64{2, 10, 50, 20, 1, 20, 1}},
		{"a bin past the last", []int64{1, 0, 0, maxBin + 1, 1}},
		{"a bin of no samples", []int64{1, 0, 0, 20, 0}},
		{"no samples at all", []int64{0, 0, 0, 20, 1}},
		{"a negative sum", []int64{1, -1, 0, 20, 1}},
		{"a negative slowest", []int64{1, 0, -1, 20, 1}},
	} {
		if _, ok := decodeHistogram(tc.list); ok {
			t.Errorf("%s: the decoder accepted %v", tc.name, tc.list)
		}
		for class := 0; class < ttfbClasses; class++ {
			lists := [ttfbClasses][]int64{{}, {}, {}}
			lists[class] = tc.list
			fb, e := json.Marshal(lists)
			if e != nil {
				t.Fatal(e)
			}
			if e := json.Unmarshal([]byte(fbDoc(string(fb))), New(MaxLimit)); e == nil {
				t.Errorf("%s: class %d was read: %s", tc.name, class, fb)
			}
		}
	}
}

// The reader accepts the shapes the decoder can read: all three lists
// empty, which is no first byte at all, and a list of one sample that took
// under a millisecond (bin 0 holds it). A document of three empty lists is
// not one the writer emits, so writing it back drops the key.
func TestFirstByteListsTheReaderAccepts(t *testing.T) {
	for _, tc := range []struct {
		fb   string
		want [ttfbClasses]int64 // samples by class
	}{
		{`[[],[],[]]`, [ttfbClasses]int64{}},
		{`[[1,0,0,0,1],[],[]]`, [ttfbClasses]int64{1, 0, 0}},
		{`[[],[2,10,50,20,1,24,1],[]]`, [ttfbClasses]int64{0, 2, 0}},
		{`[[],[],[3,6,3,0,1,3,2]]`, [ttfbClasses]int64{0, 0, 3}},
	} {
		r := New(MaxLimit)
		if e := json.Unmarshal([]byte(fbDoc(tc.fb)), r); e != nil {
			t.Errorf("%s refused: %v", tc.fb, e)
			continue
		}
		b := r.series["a"].buckets[0]
		for class, h := range b.ttfb {
			if h.n != tc.want[class] {
				t.Errorf("%s: class %d holds %d samples, want %d", tc.fb, class, h.n, tc.want[class])
			}
		}
		again, e := json.Marshal(r)
		if e != nil {
			t.Fatal(e)
		}
		if has := strings.Contains(string(again), `"fb"`); has != (tc.fb != `[[],[],[]]`) {
			t.Errorf("%s written back as %s", tc.fb, again)
		}
		if back := New(MaxLimit); json.Unmarshal(again, back) != nil {
			t.Errorf("%s: what was written back cannot be read", tc.fb)
		}
	}
}

// A file may name any moment a time.Time can encode and none beyond: the
// bound is tight to the second, a store holding the latest moment still
// encodes every availability it reports, and one moment later is refused
// wherever a moment can appear.
func TestTheReaderBoundsMomentsByWhatTimeEncodes(t *testing.T) {
	if _, e := json.Marshal(time.UnixMilli(maxMillis).UTC()); e != nil {
		t.Fatal("the bound itself does not encode:", e)
	}
	if _, e := json.Marshal(time.UnixMilli(maxMillis + 1000).UTC()); e == nil {
		t.Fatal("the bound is a second short of the latest moment that encodes")
	}
	last := maxMillis / 1000 / 300 * 300 // the latest five-minute bucket start
	doc := func(first, updated, open, start int64) string {
		return fmt.Sprintf(`{"schema":1,"series":[{"k":"a","f":%d,"u":%d,"o":[%d,%d,0],"b":[{"s":%d,"w":300,"t":[0,0,0,0],"n":[1,0,0,0,0]}]}]}`, first, updated, open, open, start)
	}
	now := t0.Add(24 * time.Hour)
	r := New(MaxLimit)
	if e := json.Unmarshal([]byte(doc(maxMillis, maxMillis, maxMillis, last)), r); e != nil {
		t.Fatal("the latest moments are refused:", e)
	}
	r.limit = r.Size() / 2
	for _, settled := range []bool{false, true} {
		if settled {
			r.Settle(now)
		}
		for _, a := range r.Availability("a", now) {
			if _, e := json.Marshal(a); e != nil {
				t.Fatalf("settled=%v: availability %s does not encode: %v", settled, a.Window, e)
			}
		}
		if _, e := json.Marshal(r.Buckets("a", now.Add(-24*time.Hour), now, HourWidth, now)); e != nil {
			t.Fatalf("settled=%v: slots do not encode: %v", settled, e)
		}
	}
	for name, bad := range map[string]string{
		"first":   doc(maxMillis+1, 0, 1, 300),
		"updated": doc(0, maxMillis+1, 1, 300),
		"open":    doc(0, 0, maxMillis+1, 300),
		"start":   doc(0, 0, 1, last+300),
		"before":  doc(0, 0, 1, -300),
	} {
		if e := json.Unmarshal([]byte(bad), New(MaxLimit)); e == nil {
			t.Errorf("a moment past the bound in %s was accepted: %s", name, bad)
		}
	}
}

// seedStore is a store holding every kind of bucket the reader can meet: a
// day, an hour and five minutes wide, with passing and failing samples,
// first byte in each class, colo counts and cache counts.
func seedStore() *Store {
	r := New(MaxLimit)
	now := t0.Add(60 * 24 * time.Hour)
	for _, d := range []time.Duration{0, 7 * 24 * time.Hour, 20*24*time.Hour + 3*time.Hour, 45 * 24 * time.Hour} {
		r.Add("k/c", "obs", t0.Add(d), "ok", 30, 10*time.Minute, now, Extra{FirstByteMS: 25, HasFirstByte: true, HasEdge: true, Cache: "HIT", Pop: "LAX"})
		r.Add("k/c", "obs", t0.Add(d+time.Minute), "fail", 0, 10*time.Minute, now, Extra{HasEdge: true, Cache: "MISS", Pop: "HKG"})
	}
	edgeTail(r, "k/c", now)
	r.Add("k/plain", "obs", now.Add(-time.Minute), "ok", 12, 10*time.Minute, now, Extra{})
	r.Settle(now)
	return r
}

// A document the reader refuses leaves the store as it was; one it accepts
// yields a store that every read of it and every write survives, before and
// after it sheds, whose reads all encode as JSON (a moment out of the range
// of time.Time would fail there, in a snapshot), whose size is never
// negative and bounds what it encodes, and that writes what it can read
// again. Seeds are a populated store, the same without the label keys,
// truncations of it, the empty documents, the latest moments and every
// known-corrupt document.
func FuzzStoreUnmarshalJSON(f *testing.F) {
	raw, e := json.Marshal(seedStore())
	if e != nil {
		f.Fatal(e)
	}
	f.Add(raw)
	f.Add(withoutLabels(f, raw))
	f.Add(raw[:len(raw)/2])
	f.Add(bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":3`), 1))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":1,"series":[]}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"schema":1,"series":null}`))
	f.Add([]byte(fbDoc(`[[],[],[]]`)))
	f.Add([]byte(fbDoc(`[[1,0,0,0,1],[],[]]`)))
	f.Add([]byte(fmt.Sprintf(`{"schema":1,"series":[{"k":"a","f":%d,"u":%d,"o":[%d,60000,0],"b":[]}]}`, maxMillis, maxMillis, maxMillis)))
	f.Add([]byte(fmt.Sprintf(`{"schema":1,"series":[{"k":"a","f":%d,"u":0,"b":[]}]}`, maxMillis+1)))
	for _, bad := range corruptStores {
		f.Add([]byte(bad))
	}
	now := t0.Add(60 * 24 * time.Hour)
	// readAll reads every way the store is read and fails on a panic or on
	// anything that does not encode as JSON.
	readAll := func(t *testing.T, r *Store, doc []byte) {
		t.Helper()
		encodes := func(what string, v any) {
			if _, e := json.Marshal(v); e != nil {
				t.Fatalf("%s does not encode: %v: %q", what, e, doc)
			}
		}
		for _, key := range r.Keys() {
			for _, from := range []time.Time{time.UnixMilli(0), now.Add(-24 * time.Hour)} {
				got, since := r.Window(key, from, now, now)
				encodes("a window", got)
				encodes("a window's since", since)
			}
			encodes("availability", r.Availability(key, now))
			encodes("since", r.Since(key))
			for _, tc := range []struct{ step, span time.Duration }{{FineWidth, 24 * time.Hour}, {HourWidth, 7 * 24 * time.Hour}, {DayWidth, 90 * 24 * time.Hour}} {
				encodes("slots", r.Buckets(key, now.Add(-tc.span), now, tc.step, now))
			}
		}
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		r := New(MaxLimit)
		r.Add("kept", "obs", now.Add(-time.Hour), "ok", 5, time.Hour, now, Extra{})
		before, kept := r.Size(), r.Keys()
		if e := r.UnmarshalJSON(doc); e != nil {
			if r.Size() != before || strings.Join(r.Keys(), ",") != strings.Join(kept, ",") {
				t.Fatalf("a refused document changed the store: %q", doc)
			}
			return
		}
		if r.Size() < 0 {
			t.Fatalf("size %d is negative: %q", r.Size(), doc)
		}
		readAll(t, r, doc)
		written, e := r.MarshalJSON()
		if e != nil {
			t.Fatalf("a store that was read cannot be written: %v: %q", e, doc)
		}
		if int64(len(written)) > r.Size() {
			t.Fatalf("encoded size %d exceeds its bound %d: %q", len(written), r.Size(), doc)
		}
		var streamed bytes.Buffer
		if e := r.WriteJSON(&streamed); e != nil {
			t.Fatalf("streaming a store that was read failed: %v: %q", e, doc)
		}
		back := New(MaxLimit)
		if e := back.UnmarshalJSON(written); e != nil {
			t.Fatalf("what a store wrote cannot be read: %v\n%s", e, written)
		}
		if again, _ := back.MarshalJSON(); !bytes.Equal(again, written) {
			t.Fatalf("rewriting changed it:\n%s\n%s", written, again)
		}
		// Settling under a cap half its size sheds, which can move a
		// series' first moment to its open sample's.
		r.limit = r.Size() / 2
		r.Settle(now)
		if r.Size() < 0 {
			t.Fatalf("size %d is negative after settling: %q", r.Size(), doc)
		}
		readAll(t, r, doc)
		if _, e := r.MarshalJSON(); e != nil {
			t.Fatalf("a settled store cannot be written: %v: %q", e, doc)
		}
	})
}
