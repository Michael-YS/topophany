package cloudflare

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// tunnelResponse reads only the documented fields this adapter reports.
// origin_ip, connection and client identifiers, the tunnel name and the
// account tag are deliberately absent: they never enter a fact even in
// memory beyond the JSON decoder's ignored fields.
type tunnelResponse struct {
	Success *bool `json:"success"`
	Result  *struct {
		Status      string `json:"status"`
		Connections []struct {
			ClientVersion      string `json:"client_version"`
			ColoName           string `json:"colo_name"`
			IsPendingReconnect bool   `json:"is_pending_reconnect"`
			OpenedAt           string `json:"opened_at"`
		} `json:"connections"`
	} `json:"result"`
}

// Closed tunnel statuses, per Cloudflare's Get a Cloudflare Tunnel reference.
const (
	tunnelHealthy  = "healthy"
	tunnelDegraded = "degraded"
	tunnelDown     = "down"
	tunnelInactive = "inactive"
)

// collectTunnel turns the tunnel read into facts and declared-state policies.
// The connection list is counted as the API returns it; Cloudflare documents
// that this deprecated array will start returning empty in favour of the
// dedicated connections endpoint, so a count of zero can mean the field moved
// rather than that no connector is live. tunnel_status carries the API's own
// word either way.
func collectTunnel(body []byte, options model.CloudflareOptions, o *model.Observation) error {
	var doc tunnelResponse
	if err := decodeJSON(body, &doc); err != nil {
		return err
	}
	if err := checkEnvelope(doc.Success); err != nil {
		return err
	}
	if doc.Result == nil {
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	switch doc.Result.Status {
	case tunnelHealthy, tunnelDegraded, tunnelDown, tunnelInactive:
		o.Facts["tunnel_status"] = doc.Result.Status
	default:
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	o.Outcome = "ok"

	connections := len(doc.Result.Connections)
	pending := 0
	colos, versions := map[string]bool{}, map[string]bool{}
	var oldest time.Time
	for _, c := range doc.Result.Connections {
		if c.IsPendingReconnect {
			pending++
		}
		if model.IsColo(c.ColoName) {
			colos[c.ColoName] = true
		}
		if versionToken.MatchString(c.ClientVersion) {
			versions[c.ClientVersion] = true
		}
		if opened, err := time.Parse(time.RFC3339Nano, c.OpenedAt); err == nil && (oldest.IsZero() || opened.Before(oldest)) {
			oldest = opened
		}
	}
	o.Facts["tunnel_connections"] = strconv.Itoa(connections)
	o.Facts["tunnel_pending_reconnect"] = strconv.Itoa(pending)
	allColos := sortedTokens(colos)
	if len(allColos) > 0 {
		fact := allColos
		if len(fact) > MaxTunnelColos {
			fact = fact[:MaxTunnelColos]
		}
		o.Facts["tunnel_colos"] = strings.Join(fact, ",")
	}
	if list := sortedCapped(versions, MaxConnectionVersions); len(list) > 0 {
		o.Facts["connector_versions"] = strings.Join(list, ",")
	}
	if !oldest.IsZero() {
		o.Facts["oldest_connection_at"] = oldest.UTC().Format(time.RFC3339)
	}

	if want := options.ExpectedConnections; want != nil && connections < *want {
		o.Outcome, o.Code = "policy", "connections_below_expected"
		o.Detail = "the tunnel has fewer live connections than the deployment declares"
		return nil
	}
	// Compare every distinct reported colo, not only the ones the capped fact
	// can show: the ninth colo is still a connection outside a declared list.
	if outsideColos(allColos, options.ExpectedColos) {
		o.Outcome, o.Code = "policy", "colo_outside_expected"
		o.Detail = "a tunnel connection is established at a colo outside the declared list"
	}
	return nil
}

// sortedTokens returns the distinct tokens in sorted order. The fact caps the
// list it prints; the colo policy compares this full set.
func sortedTokens(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for token := range set {
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}

// sortedCapped returns the distinct tokens in sorted order, keeping the first
// limit when the report carries more.
func sortedCapped(set map[string]bool, limit int) []string {
	out := sortedTokens(set)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// outsideColos reports whether a reported connection sits outside the
// declared list. An empty report or an empty declaration asserts nothing.
func outsideColos(reported, expected []string) bool {
	if len(reported) == 0 || len(expected) == 0 {
		return false
	}
	allowed := make(map[string]bool, len(expected))
	for _, colo := range expected {
		allowed[colo] = true
	}
	for _, colo := range reported {
		if !allowed[colo] {
			return true
		}
	}
	return false
}
