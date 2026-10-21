// Package source defines the small static contract between a bounded source
// runtime and an external adapter. Adapters own endpoints, authentication
// protocol, version parsing, pagination and field mapping; the runtime owns
// transport, credentials, page/byte/object budgets and cancellation.
package source

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Error is a bounded adapter/runtime failure. Collectors turn it into a
// collection-attempt diagnostic, never into a target outage.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func NewError(code, detail string) error {
	code = safeCode(code)
	if code == "" {
		code = "source_error"
	}
	return &Error{Code: code, Detail: secureText(detail, 256)}
}

// Code returns a bounded error code.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Code != "" {
		return safeCode(e.Code)
	}
	return "source_collection_failed"
}

func safeCode(s string) string {
	if len(s) < 1 || len(s) > 64 || secure.Text(s) != s || strings.TrimSpace(s) != s {
		return ""
	}
	for _, r := range s {
		if r < 0x21 || r == 0x7f || r == '/' || r == '\\' {
			return ""
		}
	}
	return s
}

func secureText(s string, limit int) string {
	if len(s) > limit {
		s = s[:limit]
	}
	if secure.Text(s) != s {
		s = "unprintable diagnostic"
	}
	return s
}

// Reading is one upstream object's normalized report. Object is the stable
// key the adapter read; a report may omit its object to be stamped with it.
type Reading struct {
	Object string
	Report model.ExternalReport
}

// Response is a bounded HTTP response supplied by the runtime.
type Response struct {
	Status      int
	ContentType string
	Body        []byte
}

// IO is the only network and credential access an adapter receives. It reads
// one configured source; it does not allow arbitrary hosts, redirects,
// ambient proxies or unbounded bodies.
type IO interface {
	Credential(context.Context, model.Source) (string, error)
	Get(context.Context, model.Source, string, map[string]string, int64) (Response, error)
	Pages() int
}

// Adapter is deliberately static and small. Validate runs before a process
// starts collection; Collect performs one source-level read for the wanted
// stable upstream object IDs.
type Adapter interface {
	Validate(model.Source) error
	Collect(context.Context, IO, model.Source, []string) ([]Reading, error)
}

// Runtime is an explicit map of adapter types to implementations. There is no
// init-time registration, plugin loading or fallback: a config naming an
// unwired adapter fails before any source is read.
type Runtime struct {
	Adapters map[string]Adapter
	NewIO    func(model.Config, model.Source) (IO, error)
}

// Validate refuses unwired/invalid sources before scheduling begins.
func (r Runtime) Validate(c model.Config) error {
	for _, x := range c.Checks {
		if x.Kind != "external" || x.External == nil {
			continue
		}
		s, ok := config.Source(c, x.External.Source)
		if !ok {
			return fmt.Errorf("source %q is not declared", x.External.Source)
		}
		a, ok := r.Adapters[s.Adapter]
		if !ok || a == nil {
			return fmt.Errorf("source %s: adapter %q is not wired", s.ID, s.Adapter)
		}
		if e := a.Validate(s); e != nil {
			return fmt.Errorf("source %s: %w", s.ID, e)
		}
		if r.NewIO == nil {
			return fmt.Errorf("source %s: no controlled IO is wired", s.ID)
		}
	}
	return nil
}

// Collect executes one adapter read and validates its bounded result. Only
// wanted object IDs leave the runtime; upstream data cannot invent bindings.
func (r Runtime) Collect(ctx context.Context, c model.Config, s model.Source, wanted []string) ([]Reading, error) {
	a, ok := r.Adapters[s.Adapter]
	if !ok || a == nil {
		return nil, NewError("adapter_unavailable", "the source adapter is not wired")
	}
	if len(wanted) == 0 || len(wanted) > s.MaxObjects {
		return nil, NewError("object_budget", "wanted object count is outside the source budget")
	}
	io, err := r.NewIO(c, s)
	if err != nil {
		return nil, err
	}
	readings, err := a.Collect(ctx, io, s, slices.Clone(wanted))
	if err != nil {
		return nil, err
	}
	if io.Pages() > s.MaxPages {
		return nil, NewError("page_budget", "adapter exceeded max_pages")
	}
	if len(readings) > s.MaxObjects {
		return nil, NewError("object_budget", "adapter exceeded max_objects")
	}
	wantedSet := make(map[string]bool, len(wanted))
	for _, id := range wanted {
		wantedSet[id] = true
	}
	seen := map[string]bool{}
	out := make([]Reading, 0, len(wanted))
	for _, reading := range readings {
		if !wantedSet[reading.Object] {
			// An unbound upstream object is ignored, not turned into topology.
			continue
		}
		if seen[reading.Object] {
			return nil, NewError("duplicate_object", "adapter returned one object twice")
		}
		seen[reading.Object] = true
		if reading.Report.Object == "" {
			reading.Report.Object = reading.Object
		}
		if reading.Report.Object != reading.Object {
			return nil, NewError("report_object_mismatch", "report object does not match the read object")
		}
		normalized, e := report.Normalize(reading.Report, s.MaxReportBytes)
		if e != nil {
			return nil, NewError(report.Code(e), "adapter report failed normalization")
		}
		reading.Report = normalized
		out = append(out, reading)
	}
	return out, nil
}
