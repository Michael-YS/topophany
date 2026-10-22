// Package beszel reads bounded, read-only PocketBase records from a Beszel Hub.
// It implements the shared source contract; transport and credential lookup
// remain owned by the caller's source.IO.
package beszel

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// Adapter collects the latest one-minute resource record for each bound
// system, and optionally its current status as a diagnostic-only report.
type Adapter struct{}

var _ source.Adapter = Adapter{}

const (
	maxResponseBytes  = 8 << 20
	maxJSONMembers    = 128
	maxJSONArray      = 4096
	maxJSONContainers = 1024
)

// Validate permits an HTTPS Hub base URL, including a simple path prefix.
// Plain HTTP is limited to a literal loopback address over the direct route;
// neither a named proxy route nor a remote resolution override may expose
// the PocketBase token on a plaintext hop.
func (Adapter) Validate(s model.Source) error {
	u, err := url.Parse(s.Target)
	if err != nil || len(s.Target) > model.MaxSourceTarget || secure.Text(s.Target) != s.Target ||
		u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(s.Target, "#") || u.RawPath != "" ||
		(u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("beszel target must be an HTTP(S) Hub base URL without credentials, query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return errors.New("beszel target port must be 1..65535")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return errors.New("beszel target port must be 1..65535")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return errors.New("beszel target path must not contain dot segments")
		}
		for _, c := range segment {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
				return errors.New("beszel target path must contain only unescaped URL path segments")
			}
		}
	}
	if strings.Contains(u.Path, "//") || strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/api") {
		return errors.New("beszel target must be the Hub base URL before /api")
	}
	if !environmentName(s.CredentialEnv) {
		return errors.New("beszel credential_env must name an environment variable")
	}
	if u.Scheme == "http" && (!loopback(u.Hostname()) || s.Path != "" || s.ResolveIP != "" && !loopback(s.ResolveIP)) {
		return errors.New("beszel requires HTTPS except for direct literal loopback HTTP")
	}
	return nil
}

func environmentName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for i, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func loopback(host string) bool {
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && ip.IsLoopback()
}

func recordID(id string) bool {
	if len(id) != 15 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Collect reserves every requested read before asking for a credential or
// making a request. A failure returns no readings, even if an earlier request
// succeeded; callers can never mistake a partial batch for a complete one.
func (a Adapter) Collect(ctx context.Context, io source.IO, s model.Source, requests []source.Request) ([]source.Reading, error) {
	if a.Validate(s) != nil {
		return nil, failure("beszel_config")
	}
	if len(requests) == 0 || s.MaxObjects < 1 || s.MaxObjects > model.MaxSourceObjects || len(requests) > s.MaxObjects {
		return nil, failure("object_budget")
	}
	seen := make(map[string]bool, len(requests))
	pages := 0
	for _, request := range requests {
		if !recordID(request.Object) || seen[request.Object] {
			return nil, failure("invalid_object_request")
		}
		seen[request.Object] = true
		if len(request.Reports) < 1 || len(request.Reports) > 2 {
			return nil, failure("unsupported_report_type")
		}
		for i, report := range request.Reports {
			if report != model.ReportTypeResource && report != model.ReportTypeStatus || i > 0 && report == request.Reports[0] {
				return nil, failure("unsupported_report_type")
			}
			pages++
		}
	}
	usedPages := io.Pages()
	if s.MaxPages < 1 || s.MaxPages > model.MaxSourcePages || usedPages < 0 || usedPages > s.MaxPages || pages > s.MaxPages-usedPages {
		return nil, failure("page_budget")
	}
	if s.MaxResponseBytes < 1 || s.MaxResponseBytes > maxResponseBytes {
		return nil, failure("response_budget")
	}
	if ctx.Err() != nil {
		return nil, failure("source_canceled")
	}
	token, err := io.Credential(ctx, s)
	if err != nil {
		return nil, ioFailure(ctx, err)
	}
	if len(token) < 1 || len(token) > 1024 {
		return nil, failure("beszel_credential")
	}
	for _, c := range token {
		if c < 0x21 || c > 0x7e {
			return nil, failure("beszel_credential")
		}
	}
	// PocketBase consumes the raw auth token. It is not an OAuth Bearer header.
	headers := map[string]string{"Authorization": token, "Accept": "application/json"}
	base := strings.TrimRight(s.Target, "/") + "/api/collections/"
	readings := make([]source.Reading, 0, pages)
	for _, request := range requests {
		if request.Wants(model.ReportTypeResource) {
			query := url.Values{
				"filter":    {`system = "` + request.Object + `" && type = "1m"`},
				"sort":      {"-created,-id"},
				"page":      {"1"},
				"perPage":   {"1"},
				"skipTotal": {"true"},
				"fields":    {"id,system,type,created,stats"},
			}
			body, _, err := get(ctx, io, s, base+"system_stats/records?"+query.Encode(), headers, false)
			if err != nil {
				return nil, err
			}
			r, err := resource(body, request.Object)
			if err != nil {
				return nil, err
			}
			if r != nil {
				// IDs are closed-shape tokens, but a peer can still reflect a
				// credential or a 15-byte run of it in an otherwise valid ID.
				if strings.Contains(r.SampleID, token) || strings.Contains(token, r.SampleID) {
					return nil, failure("beszel_record_identity")
				}
				readings = append(readings, source.Reading{Object: request.Object, Report: *r})
			}
		}
		if request.Wants(model.ReportTypeStatus) {
			query := url.Values{"fields": {"id,status,updated"}}
			body, absent, err := get(ctx, io, s, base+"systems/records/"+request.Object+"?"+query.Encode(), headers, true)
			if err != nil {
				return nil, err
			}
			if absent {
				continue
			}
			r, err := status(body, request.Object)
			if err != nil {
				return nil, err
			}
			readings = append(readings, source.Reading{Object: request.Object, Report: r})
		}
	}
	if ctx.Err() != nil {
		return nil, failure("source_canceled")
	}
	return readings, nil
}

func get(ctx context.Context, io source.IO, s model.Source, target string, headers map[string]string, allowMissing bool) ([]byte, bool, error) {
	if ctx.Err() != nil {
		return nil, false, failure("source_canceled")
	}
	response, err := io.Get(ctx, s, target, headers, s.MaxResponseBytes)
	if err != nil {
		return nil, false, ioFailure(ctx, err)
	}
	if ctx.Err() != nil {
		return nil, false, failure("source_canceled")
	}
	if int64(len(response.Body)) > s.MaxResponseBytes {
		return nil, false, failure("response_budget")
	}
	switch response.Status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, false, failure("beszel_auth")
	case http.StatusNotFound:
		if allowMissing {
			return nil, true, nil
		}
		return nil, false, failure("beszel_api_unavailable")
	case http.StatusTooManyRequests:
		return nil, false, failure("beszel_rate_limited")
	default:
		return nil, false, failure("beszel_http")
	}
	if response.ContentType != "" {
		mediaType, _, err := mime.ParseMediaType(response.ContentType)
		if err != nil || mediaType != "application/json" {
			return nil, false, failure("beszel_content_type")
		}
	}
	return response.Body, false, nil
}

// Never include upstream bodies, URLs, credential values or arbitrary I/O
// error text in an adapter error. Only fixed runtime error codes propagate.
func ioFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return failure("source_canceled")
	}
	switch code := source.Code(err); code {
	case "credential_unavailable", "wrong_source", "page_budget", "response_budget", "transport_error",
		"invalid_source_url", "cross_origin", "header_budget", "invalid_header", "invalid_source_request":
		return failure(code)
	default:
		return failure("source_collection_failed")
	}
}

func failure(code string) error {
	return source.NewError(code, "Beszel source data could not be collected")
}

func jsonBudget(body []byte) error {
	if len(body) > maxResponseBytes || !utf8.Valid(body) || !validUnicodeEscapes(body) || secure.JSONResponseBudget(body, maxJSONMembers, maxJSONArray, maxJSONContainers) != nil {
		return failure("beszel_json")
	}
	return nil
}

type statsRecord struct {
	ID      string          `json:"id"`
	System  string          `json:"system"`
	Type    string          `json:"type"`
	Created string          `json:"created"`
	Stats   json.RawMessage `json:"stats"`
}

func resource(body []byte, object string) (*model.ExternalReport, error) {
	if err := jsonBudget(body); err != nil {
		return nil, err
	}
	var page struct {
		Page       *int
		PerPage    *int
		TotalItems *int
		TotalPages *int
		Items      []json.RawMessage
	}
	if decodeObjectFields(body, map[string]any{
		"page": &page.Page, "perPage": &page.PerPage, "totalItems": &page.TotalItems,
		"totalPages": &page.TotalPages, "items": &page.Items,
	}) != nil {
		return nil, failure("beszel_json")
	}
	// skipTotal avoids counting the entire history on every poll. PocketBase
	// uses -1 for both unknown totals, including for an empty list. The item
	// read must not depend on a separate, non-atomic historical count query.
	if page.Page == nil || *page.Page != 1 || page.PerPage == nil || *page.PerPage != 1 ||
		page.TotalItems == nil || *page.TotalItems != -1 || page.TotalPages == nil || *page.TotalPages != -1 ||
		page.Items == nil || len(page.Items) > 1 {
		return nil, failure("beszel_pagination")
	}
	if len(page.Items) == 0 {
		// An empty visible list can also mean the caller lacks list access.
		// It is not evidence that the monitored system is down.
		return nil, nil
	}
	var r statsRecord
	if decodeObjectFields(page.Items[0], map[string]any{
		"id": &r.ID, "system": &r.System, "type": &r.Type,
		"created": &r.Created, "stats": &r.Stats,
	}) != nil {
		return nil, failure("beszel_json")
	}
	if !recordID(r.ID) || !recordID(object) || r.System != object || r.Type != "1m" {
		return nil, failure("beszel_record_identity")
	}
	at, err := sampleTime(r.Created)
	if err != nil {
		return nil, err
	}
	values, fields, err := decodeStatsValidated(r.Stats)
	if err != nil {
		return nil, err
	}
	return &model.ExternalReport{
		ReportMeta: model.ReportMeta{
			Schema: model.ReportSchema, Object: object, SampleID: r.ID,
			ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample, Fields: fields,
		},
		Resource: values,
	}, nil
}

// Go's time.Parse accepts single-digit hours, comma fractions, overlong
// fractions and offsets such as +24:00. Those are not this wire format.
var sampleTimeFormat = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

func sampleTime(raw string) (time.Time, error) {
	if len(raw) > 35 || !sampleTimeFormat.MatchString(raw) {
		return time.Time{}, failure("beszel_sample_time")
	}
	// PocketBase uses one space and UTC Z. RFC3339 additionally permits a
	// numeric offset. Neither representation borrows the collection clock.
	if raw[10] == ' ' {
		if raw[len(raw)-1] != 'Z' {
			return time.Time{}, failure("beszel_sample_time")
		}
		raw = raw[:10] + "T" + raw[11:]
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		at = at.UTC()
		if !at.IsZero() && at.Year() >= 1 && at.Year() <= 9999 {
			return at, nil
		}
	}
	return time.Time{}, failure("beszel_sample_time")
}

func status(body []byte, object string) (model.ExternalReport, error) {
	if err := jsonBudget(body); err != nil {
		return model.ExternalReport{}, err
	}
	var record struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if decodeObjectFields(body, map[string]any{"id": &record.ID, "status": &record.Status}) != nil {
		return model.ExternalReport{}, failure("beszel_json")
	}
	if !recordID(object) || record.ID != object {
		return model.ExternalReport{}, failure("beszel_record_identity")
	}
	state := "unknown"
	switch record.Status {
	case "up":
		state = "up"
	case "down":
		state = "down"
	}
	// systems.updated changes on metadata edits too. It is not an agent
	// observation time, so this endpoint supplies diagnostics without fresh
	// availability or latency evidence, even when status is "up" or "down".
	return model.ExternalReport{
		ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: object, TimeBasis: model.TimeBasisUnknown},
		Status:     &model.StatusReport{Status: state},
	}, nil
}
