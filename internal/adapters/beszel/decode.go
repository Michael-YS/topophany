package beszel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"

	"github.com/WhitenWhiten/topophany/internal/model"
)

const gib = float64(1 << 30)

type stats map[string]json.RawMessage

// Missing and invalid numbers remain nil. Known values are implicit in the
// report payload; unavailable fields have explicit availability metadata.
func availability(fields map[string]string, key, state string) {
	if state != model.FieldKnown {
		fields[key] = state
	}
}

func missing(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func number(raw json.RawMessage) (*float64, string) {
	if missing(raw) {
		return nil, model.FieldMissing
	}
	var v float64
	if json.Unmarshal(raw, &v) != nil || math.IsNaN(v) || math.IsInf(v, 0) || math.Signbit(v) {
		return nil, model.FieldInvalid
	}
	return &v, model.FieldKnown
}

func percent(raw json.RawMessage) (*float64, string) {
	v, state := number(raw)
	if v != nil && *v > 100 {
		return nil, model.FieldInvalid
	}
	return v, state
}

func gibValue(v *float64, state string, capacity bool) (*uint64, string) {
	if v == nil {
		return nil, state
	}
	// Check the rounded result before conversion: converting a floating
	// value at/above 2^64 to uint64 has no useful portable meaning.
	n := math.Round(*v * gib)
	if math.IsInf(n, 0) || n >= math.Exp2(64) || capacity && n == 0 {
		return nil, model.FieldInvalid
	}
	value := uint64(n)
	return &value, model.FieldKnown
}

func byteNumber(raw json.RawMessage, capacity bool) (*uint64, string) {
	if missing(raw) {
		return nil, model.FieldMissing
	}
	var value uint64
	if json.Unmarshal(raw, &value) != nil || capacity && value == 0 {
		return nil, model.FieldInvalid
	}
	return &value, model.FieldKnown
}

func decodeStats(raw json.RawMessage) (*model.ResourceReport, map[string]string, error) {
	if err := jsonBudget(raw); err != nil {
		return nil, nil, err
	}
	return decodeStatsValidated(raw)
}

// The enclosing resource response has already passed the recursive JSON
// budget. Keep the standalone decoder checked too, without parsing the same
// JSON token tree twice in a production resource read.
func decodeStatsValidated(raw json.RawMessage) (*model.ResourceReport, map[string]string, error) {
	var s stats
	if json.Unmarshal(raw, &s) != nil || s == nil {
		return nil, nil, failure("beszel_stats")
	}
	// Do not let an empty/foreign object become a successful resource report
	// merely because the adapter can attach basis strings to it.
	recognized := false
	for _, key := range []string{"cpu", "m", "mu", "mp", "mem", "d", "du", "dp", "efs"} {
		if _, ok := s[key]; ok {
			recognized = true
			break
		}
	}
	if !recognized {
		return nil, nil, failure("beszel_empty_stats")
	}
	fields := map[string]string{
		"cpu.logical_cores":      model.FieldUnsupported,
		"cpu.sample_window_ms":   model.FieldUnsupported,
		"memory.available_bytes": model.FieldUnsupported,
	}
	cpu, state := percent(s["cpu"])
	availability(fields, "cpu.percent", state)
	res := &model.ResourceReport{CPU: &model.CPUReport{Percent: cpu, Basis: "beszel_agent_percent"}}
	res.Memory = memory(s, fields)
	if has(s, "d") || has(s, "du") || has(s, "dp") {
		res.Disks = append(res.Disks, disk("root", s, fields, false))
	}
	if !missing(s["efs"]) {
		var extra map[string]json.RawMessage
		if json.Unmarshal(s["efs"], &extra) != nil || extra == nil {
			return nil, nil, failure("beszel_filesystems")
		}
		if len(extra)+len(res.Disks) > model.MaxReportDisks {
			return nil, nil, failure("disk_budget")
		}
		keys := make([]string, 0, len(extra))
		for key := range extra {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key == "" || len(key) > 128 {
				return nil, nil, failure("beszel_filesystem_id")
			}
			var fs stats
			if json.Unmarshal(extra[key], &fs) != nil || fs == nil {
				return nil, nil, failure("beszel_filesystems")
			}
			// Beszel's map key is a device/custom name, not a mount path.
			// Hash it into a stable bounded ID: arbitrary authenticated text
			// (including a reflected credential) must not leave the adapter.
			sum := sha256.Sum256([]byte(key))
			id := "fs-" + hex.EncodeToString(sum[:])
			res.Disks = append(res.Disks, disk(id, fs, fields, true))
		}
	}
	if len(fields) > model.MaxReportFields {
		return nil, nil, failure("report_fields_budget")
	}
	return res, fields, nil
}

func has(s stats, key string) bool { _, ok := s[key]; return ok }

func memory(s stats, fields map[string]string) *model.MemoryReport {
	m := &model.MemoryReport{Basis: "beszel_gib_rounded"}
	var totalState, usedState string
	if raw, present := s["mem"]; present {
		m.Basis = "beszel_bytes"
		totalState, usedState = model.FieldMissing, model.FieldMissing
		if !missing(raw) {
			var values []json.RawMessage
			if json.Unmarshal(raw, &values) != nil || len(values) != 4 {
				totalState, usedState = model.FieldInvalid, model.FieldInvalid
			} else {
				// Decode uint64 directly so integers above 2^53 remain exact.
				m.TotalBytes, totalState = byteNumber(values[0], true)
				m.UsedBytes, usedState = byteNumber(values[1], false)
				for _, v := range values[2:] {
					if _, state := byteNumber(v, false); state != model.FieldKnown {
						m.TotalBytes, m.UsedBytes = nil, nil
						totalState, usedState = model.FieldInvalid, model.FieldInvalid
					}
				}
			}
		}
	} else {
		totalGiB, totalNumberState := number(s["m"])
		usedGiB, usedNumberState := number(s["mu"])
		m.TotalBytes, totalState = gibValue(totalGiB, totalNumberState, true)
		m.UsedBytes, usedState = gibValue(usedGiB, usedNumberState, false)
		// Compare the upstream values before byte rounding as well. A tiny
		// overrun must not become valid just because both round to one byte.
		if totalGiB != nil && usedGiB != nil && *usedGiB > *totalGiB {
			m.UsedBytes, usedState = nil, model.FieldInvalid
		}
	}
	if m.TotalBytes != nil && m.UsedBytes != nil && *m.UsedBytes > *m.TotalBytes {
		m.UsedBytes, usedState = nil, model.FieldInvalid
	}
	availability(fields, "memory.total_bytes", totalState)
	availability(fields, "memory.used_bytes", usedState)
	var percentState string
	m.Percent, percentState = percent(s["mp"])
	// Only the exact byte representation can supply a missing percent.
	// Old GiB capacities were rounded by the agent and cannot reproduce mp.
	if !has(s, "mp") && has(s, "mem") && m.TotalBytes != nil && m.UsedBytes != nil {
		v := float64(*m.UsedBytes) / float64(*m.TotalBytes) * 100
		m.Percent, percentState = &v, model.FieldKnown
	}
	availability(fields, "memory.percent", percentState)
	return m
}

func disk(id string, s stats, fields map[string]string, extra bool) model.DiskReport {
	d := model.DiskReport{ID: id, Basis: "beszel_filesystem_gib_rounded"}
	prefix := "disk." + id + "."
	totalGiB, totalNumberState := number(s["d"])
	usedGiB, usedNumberState := number(s["du"])
	total, totalState := gibValue(totalGiB, totalNumberState, true)
	used, usedState := gibValue(usedGiB, usedNumberState, false)
	if totalGiB != nil && usedGiB != nil && *usedGiB > *totalGiB {
		used, usedState = nil, model.FieldInvalid
	}
	d.TotalBytes, d.UsedBytes = total, used
	availability(fields, prefix+"total_bytes", totalState)
	availability(fields, prefix+"used_bytes", usedState)
	fields[prefix+"free_bytes"] = model.FieldUnsupported
	var percentState string
	if extra {
		// efs has no dp. Beszel computes its percent from the original
		// rounded GiB values, not from our further byte conversion.
		percentState = model.FieldMissing
		if totalState == model.FieldInvalid || usedState == model.FieldInvalid {
			percentState = model.FieldInvalid
		} else if total != nil && used != nil {
			v := *usedGiB / *totalGiB * 100
			if v >= 0 && v <= 100 {
				d.Percent, percentState = &v, model.FieldKnown
			} else {
				percentState = model.FieldInvalid
			}
		}
	} else {
		// Root UsedPercent accounts for filesystem semantics (e.g. reserved
		// blocks) and must not be replaced with the ratio of rounded d/du.
		d.Percent, percentState = percent(s["dp"])
	}
	availability(fields, prefix+"percent", percentState)
	return d
}
