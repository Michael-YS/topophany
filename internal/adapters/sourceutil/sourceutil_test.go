package sourceutil_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/beszel"
	"github.com/WhitenWhiten/topophany/internal/adapters/komari"
	"github.com/WhitenWhiten/topophany/internal/adapters/sourceutil"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

func TestAdaptersShareURLSafetyWithoutChangingCredentialPolicy(t *testing.T) {
	for name, adapter := range map[string]source.Adapter{"beszel": beszel.Adapter{}, "komari": komari.Adapter{}} {
		t.Run(name, func(t *testing.T) {
			s := model.Source{Target: "https://monitor.example.test/prefix", CredentialEnv: "SYNTHETIC_TOKEN"}
			for _, target := range []string{"https://monitor.example.test", "https://monitor.example.test:65535/prefix/", "http://127.0.0.1", "http://[::1]", "http://[::ffff:127.0.0.1]"} {
				s.Target = target
				if err := adapter.Validate(s); err != nil {
					t.Fatalf("valid base URL refused: %v", err)
				}
			}
			for _, target := range []string{
				"", "ftp://monitor.example.test", "https:opaque", "https://user:synthetic-secret@monitor.example.test",
				"https://monitor.example.test?synthetic-secret", "https://monitor.example.test?", "https://monitor.example.test#synthetic-secret",
				"https://monitor.example.test#", "https://monitor.example.test:0", "https://monitor.example.test:65536", "https://monitor.example.test:",
				"https://monitor.example.test/a/../b", "https://monitor.example.test/a/./b", "https://monitor.example.test/a//b",
				"https://monitor.example.test/a%2fb", "https://monitor.example.test/escaped%61", "https://monitor.example.test/hub/api/",
				"https://monitor.example.test/非ASCII", "http://localhost", "http://127.1", "http://2130706433", "http://[::1%25lo0]",
				"http://127.0.0.1.evil.test", "http://192.0.2.1", strings.Repeat("x", model.MaxSourceTarget+1),
			} {
				s.Target = target
				err := adapter.Validate(s)
				if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
					t.Fatal("unsafe URL admitted or reflected in a diagnostic")
				}
			}
			s.Target = "http://127.0.0.1"
			s.Path = "explicit-proxy"
			if adapter.Validate(s) == nil {
				t.Fatal("plaintext proxy route admitted")
			}
			s.Path, s.ResolveIP = "", "192.0.2.1"
			if adapter.Validate(s) == nil {
				t.Fatal("plaintext non-loopback resolve override admitted")
			}
			s.ResolveIP = "::1"
			if err := adapter.Validate(s); err != nil {
				t.Fatal("direct loopback resolution refused")
			}
			s.Target, s.ResolveIP = "https://monitor.example.test", ""
			for _, env := range []string{"1BAD", "BAD-NAME", "BAD NAME", "BAD\nNAME", strings.Repeat("A", 129)} {
				s.CredentialEnv = env
				if adapter.Validate(s) == nil {
					t.Fatal("invalid credential reference admitted")
				}
			}
			s.CredentialEnv = strings.Repeat("A", 128)
			if err := adapter.Validate(s); err != nil {
				t.Fatal("maximum environment name refused")
			}
			s.CredentialEnv = ""
			if err := adapter.Validate(s); (err == nil) != (name == "komari") {
				t.Fatalf("anonymous credential policy changed: %v", err)
			}
		})
	}
}

func TestVendorCredentialLengthAndCharacterBoundaries(t *testing.T) {
	for _, limits := range [][2]int{{1, 1024}, {12, 1017}} {
		for _, n := range []int{limits[0], limits[1]} {
			if !sourceutil.ValidCredential(strings.Repeat("x", n), limits[0], limits[1]) {
				t.Fatal("inclusive credential boundary refused")
			}
		}
		for _, token := range []string{strings.Repeat("x", limits[0]-1), strings.Repeat("x", limits[1]+1), "synthetic key", "synthetic\rkey", "synthetic\nkey", "synthetic\x00key", "synthetic\x7fkey", "synthetic-é-key"} {
			if sourceutil.ValidCredential(token, limits[0], limits[1]) {
				t.Fatal("unsafe header credential admitted")
			}
		}
	}
	if sourceutil.ValidCredential("x", 0, 1) || sourceutil.ValidCredential("x", 2, 1) {
		t.Fatal("invalid length policy admitted")
	}
}

func TestCompleteSourceBudgetsAndConsumedPages(t *testing.T) {
	s := model.Source{MaxObjects: 2, MaxPages: 2, MaxResponseBytes: 1024}
	for _, tc := range []struct {
		count, maximum int
		code           string
	}{
		{1, 2, ""}, {2, 2, ""}, {0, 2, "object_budget"}, {-1, 2, "object_budget"}, {3, 2, "object_budget"},
		{1, 0, "object_budget"}, {1, -1, "object_budget"}, {1, model.MaxSourceObjects + 1, "object_budget"},
	} {
		x := s
		x.MaxObjects = tc.maximum
		if got := sourceutil.ObjectBudget(x, tc.count); got != tc.code {
			t.Fatalf("object budget = %q, want %q", got, tc.code)
		}
	}
	for _, tc := range []struct {
		used, planned int
		code          string
	}{
		{0, 2, ""}, {1, 1, ""}, {1, 2, "page_budget"}, {2, 1, "page_budget"},
		{-1, 1, "page_budget"}, {math.MaxInt, 1, "page_budget"}, {0, math.MaxInt, "page_budget"}, {0, 0, "page_budget"}, {0, -1, "page_budget"},
	} {
		if got := sourceutil.ReadBudget(s, tc.used, tc.planned); got != tc.code {
			t.Fatalf("read budget = %q, want %q", got, tc.code)
		}
	}
	for _, pages := range []int{-1, 0, model.MaxSourcePages + 1} {
		x := s
		x.MaxPages = pages
		if sourceutil.ReadBudget(x, 0, 1) != "page_budget" {
			t.Fatal("invalid page setting admitted")
		}
	}
	for _, bytes := range []int64{-1, 0, sourceutil.MaxResponseBytes + 1} {
		x := s
		x.MaxResponseBytes = bytes
		if sourceutil.ReadBudget(x, 0, 1) != "response_budget" {
			t.Fatal("invalid response setting admitted")
		}
	}
	for _, bytes := range []int64{1, sourceutil.MaxResponseBytes} {
		x := s
		x.MaxResponseBytes = bytes
		if sourceutil.ReadBudget(x, 0, 1) != "" {
			t.Fatal("inclusive response boundary refused")
		}
	}
}

func TestGuardReportRawEscapedNumericAndEncodingFailures(t *testing.T) {
	for _, token := range []string{"synthetic-api-key", `synthetic<key>&"with\escapes`, "探针凭据"} {
		r := &model.ExternalReport{Resource: &model.ResourceReport{CPU: &model.CPUReport{Basis: token}}}
		if err := sourceutil.GuardReport(r, token); !errors.Is(err, sourceutil.ErrCredentialReflection) || strings.Contains(err.Error(), token) {
			t.Fatal("raw or escaped credential escaped the report guard")
		}
		if err := sourceutil.GuardReport(r, "unrelated-synthetic-key"); err != nil {
			t.Fatal("unrelated credential blocked a report")
		}
	}
	total := uint64(9007199254740993)
	r := &model.ExternalReport{Resource: &model.ResourceReport{Memory: &model.MemoryReport{TotalBytes: &total}}}
	if !errors.Is(sourceutil.GuardReport(r, "9007199254740993"), sourceutil.ErrCredentialReflection) {
		t.Fatal("exact integer reflection escaped the guard")
	}
	invalid := math.Inf(1)
	r.Resource.CPU = &model.CPUReport{Percent: &invalid}
	if err := sourceutil.GuardReport(r, "synthetic-api-key"); err == nil || errors.Is(err, sourceutil.ErrCredentialReflection) {
		t.Fatal("encoding failure was swallowed or mislabeled as reflection")
	}
	if sourceutil.GuardReport(r, "") != nil {
		t.Fatal("anonymous collection invoked credential serialization")
	}
}

func TestJSONBudgetPreservesRepresentationAndVendorLimits(t *testing.T) {
	for _, body := range []string{`{"cpu":0,"text":"探针"}`, `{"text":"\ud83d\ude00"}`, `{"text":"\\ud800"}`} {
		if err := sourceutil.JSONBudget([]byte(body), 128, 4096, 1024); err != nil {
			t.Fatal("valid representation refused")
		}
	}
	for _, body := range []string{`{"cpu":0,"\u0063pu":1}`, `{"text":"\ud800"}`, "\"\xff\"", `{} {}`, strings.Repeat(" ", sourceutil.MaxResponseBytes+1)} {
		if sourceutil.JSONBudget([]byte(body), 128, 4096, 1024) == nil {
			t.Fatal("unsafe or oversized JSON admitted")
		}
	}
	body := []byte(`[{},{},{}]`)
	if sourceutil.JSONBudget(body, 1, 3, 4) != nil || sourceutil.JSONBudget(body, 1, 3, 3) == nil || sourceutil.JSONBudget(body, 1, 2, 4) == nil {
		t.Fatal("array or vendor-specific container limit changed")
	}
	if sourceutil.JSONBudget([]byte(`{"a":0,"b":1}`), 1, 3, 4) == nil {
		t.Fatal("object member budget ignored")
	}
}

type controlledIO struct {
	response  source.Response
	err       error
	reads     int
	afterRead func()
}

func (*controlledIO) Credential(context.Context, model.Source) (string, error) {
	panic("shared read must never resolve credentials")
}
func (*controlledIO) Pages() int { panic("shared read must not change preflight planning") }
func (f *controlledIO) Get(_ context.Context, _ model.Source, _ string, _ map[string]string, _ int64) (source.Response, error) {
	f.reads++
	if f.afterRead != nil {
		f.afterRead()
	}
	return f.response, f.err
}

func TestControlledReadCancellationBoundsAndClosedErrors(t *testing.T) {
	s := model.Source{MaxResponseBytes: 2}
	for _, tc := range []struct {
		name                       string
		cancelBefore, cancelDuring bool
		body                       string
		err                        error
		code                       string
		reads                      int
	}{
		{"exact-limit", false, false, "{}", nil, "", 1},
		{"oversize", false, false, "{} ", nil, "response_budget", 1},
		{"canceled-before", true, false, "{}", nil, "source_canceled", 0},
		{"canceled-during", false, true, "{}", nil, "source_canceled", 1},
		{"canceled-error", false, true, "", errors.New("synthetic-secret"), "source_canceled", 1},
		{"raw-error", false, false, "", errors.New("synthetic-secret"), "source_collection_failed", 1},
		{"unknown-code", false, false, "", source.NewError("synthetic-secret", "synthetic-secret"), "source_collection_failed", 1},
		{"closed-code", false, false, "", source.NewError("transport_error", "synthetic-secret"), "transport_error", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &controlledIO{response: source.Response{Status: 404, ContentType: "text/html", Body: []byte(tc.body)}, err: tc.err}
			if tc.cancelBefore {
				cancel()
			}
			if tc.cancelDuring {
				f.afterRead = cancel
			}
			response, err := sourceutil.Get(ctx, f, s, "https://monitor.example.test", nil)
			if f.reads != tc.reads {
				t.Fatalf("read count = %d", f.reads)
			}
			if tc.code == "" {
				if err != nil || response.Status != 404 || response.ContentType != "text/html" || string(response.Body) != tc.body {
					t.Fatalf("helper interpreted vendor HTTP semantics: %v", err)
				}
			} else if err == nil || source.Code(err) != tc.code || len(response.Body) != 0 || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("unsafe failure boundary: %v", err)
			}
		})
	}
	for _, code := range []string{"credential_unavailable", "wrong_source", "page_budget", "response_budget", "transport_error", "invalid_source_url", "cross_origin", "header_budget", "invalid_header", "invalid_source_request"} {
		if sourceutil.IOFailureCode(context.Background(), source.NewError(code, "synthetic-secret")) != code {
			t.Fatal("closed runtime code changed")
		}
	}
	for _, media := range []string{"", "application/json", "application/json; charset=utf-8"} {
		if !sourceutil.JSONContentType(media) {
			t.Fatal("JSON media type refused")
		}
	}
	for _, media := range []string{"text/html", "application/json; bad", "application/problem+json"} {
		if sourceutil.JSONContentType(media) {
			t.Fatal("unexpected media type admitted")
		}
	}
}
