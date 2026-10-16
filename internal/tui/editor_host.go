package tui

import (
	"fmt"
	"strconv"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// addHostFields adds a host check's options, shown while when holds.
func addHostFields(f *Form, options *model.HostOptions, when func(*Form) bool) {
	h := model.HostOptions{}
	if options != nil {
		h = *options
	}
	f.add("Host disk path", h.DiskPath, fText, "host: absolute local filesystem path; blank means /").when = when
	for _, v := range []struct {
		label string
		n     float64
	}{{"Host max CPU %", h.MaxCPUPercent}, {"Host max memory %", h.MaxMemoryPercent}, {"Host max disk %", h.MaxDiskPercent}} {
		f.add(v.label, strconv.FormatFloat(v.n, 'f', -1, 64), fText, "host: 0 disables assertion; otherwise 0..100").when = when
	}
	f.add("Host process name", h.ProcessName, fText, "host: exact name, up to 15 bytes; no command arguments").when = when
	f.add("Host min processes", strconv.Itoa(h.MinProcesses), fInt, "host: minimum non-zombie matches; 0 disables assertion").when = when
	f.add("Host max processes", strconv.Itoa(h.MaxProcesses), fInt, "host: maximum non-zombie matches; 0 disables assertion").when = when
}

func hostFields(f *Form, kind string) (*model.HostOptions, error) {
	if kind != "host" {
		return nil, nil
	}
	h := &model.HostOptions{DiskPath: f.val("Host disk path"), ProcessName: f.val("Host process name")}
	for _, v := range []struct {
		label string
		n     *float64
	}{{"Host max CPU %", &h.MaxCPUPercent}, {"Host max memory %", &h.MaxMemoryPercent}, {"Host max disk %", &h.MaxDiskPercent}} {
		raw := f.val(v.label)
		if raw == "" {
			raw = "0"
		}
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a percentage", v.label)
		}
		*v.n = n
	}
	var err error
	h.MinProcesses, err = f.num("Host min processes")
	if err != nil {
		return nil, err
	}
	h.MaxProcesses, err = f.num("Host max processes")
	return h, err
}
