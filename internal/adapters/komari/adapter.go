// Package komari reads bounded, read-only recent resource reports from a
// Komari server. The caller's source.IO owns transport and credentials.
package komari

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters/sourceutil"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// Adapter implements the Komari 1.6.0 recent-report schema. This GET endpoint
// supplies resource samples, not the separate online-presence state.
type Adapter struct{}

var _ source.Adapter = Adapter{}

const (
	maxJSONMembers    = 128
	maxJSONArray      = 4096
	maxJSONContainers = 16384
	maxRecentRecords  = 1024
)

// Validate accepts a server base URL with an optional simple path prefix.
// Public nodes can be read without a credential. A configured credential is
// always explicit; plaintext transport is restricted even for public reads.
func (Adapter) Validate(s model.Source) error {
	return sourceutil.ValidateSource(s, sourceutil.SourcePolicy{Adapter: "komari", BaseName: "server", CredentialOptional: true})
}

func objectID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Collect reserves one read per distinct requested node before obtaining any
// credential or making a request. A later failure discards the whole batch.
func (a Adapter) Collect(ctx context.Context, io source.IO, s model.Source, requests []source.Request) ([]source.Reading, error) {
	if a.Validate(s) != nil {
		return nil, failure("komari_config")
	}
	if code := sourceutil.ObjectBudget(s, len(requests)); code != "" {
		return nil, failure(code)
	}
	seen := make(map[string]bool, len(requests))
	for _, request := range requests {
		if !objectID(request.Object) || seen[request.Object] {
			return nil, failure("invalid_object_request")
		}
		seen[request.Object] = true
		if len(request.Reports) != 1 || request.Reports[0] != model.ReportTypeResource {
			return nil, failure("unsupported_report_type")
		}
	}
	if code := sourceutil.ReadBudget(s, io.Pages(), len(requests)); code != "" {
		return nil, failure(code)
	}
	if ctx.Err() != nil {
		return nil, failure("source_canceled")
	}
	headers := map[string]string{"Accept": "application/json"}
	var token string
	if s.CredentialEnv != "" {
		var err error
		token, err = io.Credential(ctx, s)
		if err != nil {
			return nil, failure(sourceutil.IOFailureCode(ctx, err))
		}
		// Komari requires API keys of at least 12 bytes; the complete header
		// must also fit the source transport's 1024-byte header-value budget.
		if !sourceutil.ValidCredential(token, 12, 1024-len("Bearer ")) {
			return nil, failure("komari_credential")
		}
		headers["Authorization"] = "Bearer " + token
	}
	base := strings.TrimRight(s.Target, "/") + "/api/recent/"
	readings := make([]source.Reading, 0, len(requests))
	for _, request := range requests {
		body, err := get(ctx, io, s, base+request.Object, headers)
		if err != nil {
			return nil, err
		}
		r, err := resource(body, request.Object)
		if err != nil {
			return nil, err
		}
		if r != nil {
			if err := credentialSafe(r, token); err != nil {
				return nil, err
			}
			readings = append(readings, source.Reading{Object: request.Object, Report: *r})
		}
	}
	if ctx.Err() != nil {
		return nil, failure("source_canceled")
	}
	return readings, nil
}

func get(ctx context.Context, io source.IO, s model.Source, target string, headers map[string]string) ([]byte, error) {
	response, err := sourceutil.Get(ctx, io, s, target, headers)
	if err != nil {
		return nil, failure(source.Code(err))
	}
	switch response.Status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, failure("komari_auth")
	case http.StatusNotFound:
		return nil, failure("komari_api_unavailable")
	case http.StatusTooManyRequests:
		return nil, failure("komari_rate_limited")
	default:
		return nil, failure("komari_http")
	}
	if !sourceutil.JSONContentType(response.ContentType) {
		return nil, failure("komari_content_type")
	}
	return response.Body, nil
}

// credentialSafe keeps vendor diagnostics local while sharing the complete
// report guard without resolving a credential for anonymous sources.
func credentialSafe(r *model.ExternalReport, token string) error {
	if err := sourceutil.GuardReport(r, token); err != nil {
		if errors.Is(err, sourceutil.ErrCredentialReflection) {
			return failure("komari_credential_reflection")
		}
		return failure("komari_report")
	}
	return nil
}

func failure(code string) error {
	return source.NewError(code, "Komari source data could not be collected")
}
