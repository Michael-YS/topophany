package beszel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

// These cases come from the accepted wire formats, rather than from Go's
// permissive time.Parse behavior. In particular, time.Parse intentionally
// accepts offsets of 24 hours/60 minutes and a comma fractional separator.
func TestBeszelSampleTimeRejectsNonCanonicalWireForms(t *testing.T) {
	for name, raw := range map[string]string{
		"offset-hour-positive":       "2026-10-22T12:34:56+24:00",
		"offset-hour-negative":       "2026-10-22T12:34:56-24:00",
		"offset-minute-positive":     "2026-10-22T12:34:56+00:60",
		"offset-minute-negative":     "2026-10-22T12:34:56-00:60",
		"single-digit-hour":          "2026-10-22T2:34:56Z",
		"comma-fraction":             "2026-10-22T12:34:56,789Z",
		"pocketbase-comma-fraction":  "2026-10-22 12:34:56,789Z",
		"subnanosecond-truncation":   "2026-10-22T12:34:56.1234567891Z",
		"multiple-separating-spaces": "2026-10-22  12:34:56.789Z",
		"utc-before-supported-year":  "0001-01-01T00:30:00+01:00",
		"utc-after-supported-year":   "9999-12-31T23:30:00-01:00",
	} {
		t.Run(name, func(t *testing.T) {
			at, err := sampleTime(raw)
			if err == nil {
				t.Fatalf("noncanonical sample time %q was accepted as %v", raw, at)
			}
			if !at.IsZero() {
				t.Fatalf("refused sample time retained an observable value: %v", at)
			}
		})
	}
}

func TestBeszelNegativeUnderflowCannotBecomeKnownZero(t *testing.T) {
	for _, raw := range []string{"-0", "-1e-999"} {
		t.Run(raw, func(t *testing.T) {
			body := json.RawMessage(fmt.Sprintf(`{"cpu":%s,"m":%s,"mu":%s,"mp":%s,"d":%s,"du":%s,"dp":%s,"efs":{"storage":{"d":8,"du":%s}}}`, raw, raw, raw, raw, raw, raw, raw, raw))
			r, fields, err := decodeStats(body)
			if err != nil {
				t.Fatalf("invalid numeric fields should remain explicit partial fields: %v", err)
			}
			if r == nil || r.CPU == nil || r.Memory == nil || len(r.Disks) != 2 {
				t.Fatalf("partial resource structure lost: %+v", r)
			}
			if r.CPU.Percent != nil || r.Memory.TotalBytes != nil || r.Memory.UsedBytes != nil || r.Memory.Percent != nil ||
				r.Disks[0].TotalBytes != nil || r.Disks[0].UsedBytes != nil || r.Disks[0].Percent != nil ||
				r.Disks[1].UsedBytes != nil || r.Disks[1].Percent != nil {
				t.Fatalf("negative input %s was converted to known zero: %+v", raw, r)
			}
			for _, field := range []string{"cpu.percent", "memory.total_bytes", "memory.used_bytes", "memory.percent", "disk.root.total_bytes", "disk.root.used_bytes", "disk.root.percent", "disk." + r.Disks[1].ID + ".used_bytes", "disk." + r.Disks[1].ID + ".percent"} {
				if fields[field] != model.FieldInvalid {
					t.Fatalf("negative input %s has %s availability %q, want invalid", raw, field, fields[field])
				}
			}
			if r.Disks[1].TotalBytes == nil || *r.Disks[1].TotalBytes != 8589934592 {
				t.Fatal("independent positive capacity was discarded with invalid usage")
			}
		})
	}
}

func TestBeszelSampleTimePreservesSupportedPrecisionAndOffset(t *testing.T) {
	want := time.Date(2026, time.October, 22, 12, 34, 56, 123456789, time.UTC)
	for _, raw := range []string{
		"2026-10-22 12:34:56.123456789Z",
		"2026-10-22T12:34:56.123456789Z",
		"2026-10-22T18:19:56.123456789+05:45",
		"2026-10-22T09:04:56.123456789-03:30",
	} {
		at, err := sampleTime(raw)
		if err != nil || !at.Equal(want) || at.Location() != time.UTC {
			t.Fatalf("sample time %q: got %v, %v; want %v in UTC", raw, at, err, want)
		}
	}
	for _, raw := range []string{"2026-10-22 12:34:56Z", "2026-10-22T12:34:56+00:00"} {
		at, err := sampleTime(raw)
		if err != nil || !at.Equal(want.Truncate(time.Second)) {
			t.Fatalf("whole-second sample time %q: got %v, %v", raw, at, err)
		}
	}
}

// Beszel v0.21.0 agent/cpu.go:getPerCoreCpuUsage emits one entry per
// logical CPU, without a 256-entry cap. This valid but unmapped field must
// not make a 512-CPU host's mapped CPU/memory/disk resource report disappear.
func TestBeszelLargePerCoreArrayDoesNotDiscardMappedResources(t *testing.T) {
	perCore := make([]int, 512)
	for i := range perCore {
		perCore[i] = i % 101
	}
	stats, err := json.Marshal(map[string]any{
		"cpu": 12.5, "cpus": perCore,
		"m": 8, "mu": 3, "mp": 37.5,
		"d": 100, "du": 20, "dp": 23.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	const object = "protocolsys0001"
	body := []byte(fmt.Sprintf(`{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[{"id":"protocolrec0001","system":%q,"type":"1m","created":"2026-10-22 12:34:56.123Z","stats":%s}]}`, object, stats))
	r, err := resource(body, object)
	if err != nil || r == nil || r.Resource == nil {
		t.Fatalf("valid large-core Beszel record was discarded: %v", err)
	}
	if _, err := report.Normalize(*r, model.MaxReportBytes); err != nil {
		t.Fatalf("large-core resource failed shared admission: %v", err)
	}
	cpu, memory, disks := r.Resource.CPU, r.Resource.Memory, r.Resource.Disks
	if cpu == nil || cpu.Percent == nil || *cpu.Percent != 12.5 || cpu.LogicalCores != nil {
		t.Fatalf("unmapped per-core data changed aggregate CPU: %+v", cpu)
	}
	if memory == nil || memory.TotalBytes == nil || *memory.TotalBytes != 8589934592 || memory.UsedBytes == nil || *memory.UsedBytes != 3221225472 {
		t.Fatalf("memory changed while accepting per-core extension: %+v", memory)
	}
	if len(disks) != 1 || disks[0].Percent == nil || *disks[0].Percent != 23.25 {
		t.Fatalf("root percentage changed while accepting per-core extension: %+v", disks)
	}
	encoded, err := json.Marshal(r)
	if err != nil || strings.Contains(string(encoded), `"cpus"`) {
		t.Fatalf("unmapped extension escaped normalized report: %v", err)
	}
}
