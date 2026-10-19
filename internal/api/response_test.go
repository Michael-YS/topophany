package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRollupsRejectOverflowingSpan(t *testing.T) {
	_, handler, _ := fixture(t)
	for _, span := range []string{"213504d", "213505d", "9999999h"} {
		w := request(handler, "GET", "/v1/rollups?check=home-http&step=5m&span="+span, viewerToken, "", nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("span %s: status %d", span, w.Code)
		}
	}
}

func TestJSONEncodingFailureDoesNotCommitSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]float64{"value": math.NaN()})
	var got map[string]string
	if w.Code != http.StatusInternalServerError || json.Unmarshal(w.Body.Bytes(), &got) != nil || got["error"] != "json_encoding_failed" {
		t.Fatal("encoding failure committed a broken success", w.Code, w.Body.String())
	}
}
