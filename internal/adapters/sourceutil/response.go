package sourceutil

import (
	"context"
	"mime"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// Get uses only the caller's controlled IO. The adapter owns HTTP status
// meaning, including whether a 404 means absence or an unavailable endpoint.
func Get(ctx context.Context, io source.IO, s model.Source, target string, headers map[string]string) (source.Response, error) {
	if ctx.Err() != nil {
		return source.Response{}, source.NewError("source_canceled", "source read canceled")
	}
	response, err := io.Get(ctx, s, target, headers, s.MaxResponseBytes)
	if err != nil {
		return source.Response{}, source.NewError(IOFailureCode(ctx, err), "source read failed")
	}
	if ctx.Err() != nil {
		return source.Response{}, source.NewError("source_canceled", "source read canceled")
	}
	if int64(len(response.Body)) > s.MaxResponseBytes {
		return source.Response{}, source.NewError("response_budget", "source response exceeded its byte budget")
	}
	return response, nil
}

// JSONContentType permits an omitted header and otherwise requires the JSON
// media type. Vendor HTTP status handling must run before this check.
func JSONContentType(contentType string) bool {
	if contentType == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

// IOFailureCode admits only fixed runtime codes, never arbitrary IO error text
// or upstream-chosen codes. Each adapter supplies its own fixed diagnostic.
func IOFailureCode(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "source_canceled"
	}
	switch code := source.Code(err); code {
	case "credential_unavailable", "wrong_source", "page_budget", "response_budget", "transport_error",
		"invalid_source_url", "cross_origin", "header_budget", "invalid_header", "invalid_source_request":
		return code
	default:
		return "source_collection_failed"
	}
}
