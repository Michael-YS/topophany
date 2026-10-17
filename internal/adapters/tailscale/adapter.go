// Package tailscale collects read-only local runtime telemetry.
package tailscale

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func Collect(ctx context.Context, command adapters.Command, options model.TailscaleOptions, o *model.Observation) error {
	b, e := command(ctx, "tailscale", "status", "--json")
	if e != nil {
		return adapters.Local("adapter", "tailscale_unavailable_or_permission_denied")
	}
	if len(b) > 2<<20 {
		return adapters.Local("adapter", "response_limit_exceeded")
	}
	var status struct {
		BackendState string `json:"BackendState"`
		Peer         map[string]struct {
			ID           string    `json:"ID"`
			HostName     string    `json:"HostName"`
			DNSName      string    `json:"DNSName"`
			TailscaleIPs []string  `json:"TailscaleIPs"`
			Online       bool      `json:"Online"`
			Active       bool      `json:"Active"`
			CurAddr      string    `json:"CurAddr"`
			Relay        string    `json:"Relay"`
			LastSeen     time.Time `json:"LastSeen"`
		} `json:"Peer"`
	}
	if e = json.Unmarshal(b, &status); e != nil || status.BackendState == "" {
		return adapters.Local("adapter", "unsupported_tailscale_response")
	}
	o.Facts["backend_state"] = adapters.SafeFact(status.BackendState)
	for key, p := range status.Peer {
		match := key == options.Peer || p.ID == options.Peer || p.HostName == options.Peer || strings.TrimSuffix(p.DNSName, ".") == strings.TrimSuffix(options.Peer, ".")
		for _, ip := range p.TailscaleIPs {
			match = match || ip == options.Peer
		}
		if !match {
			continue
		}
		o.Outcome = "ok"
		o.Stage = "adapter"
		o.PathEvidence = "peer_local_runtime_reported"
		o.Facts["online_reported"] = strconv.FormatBool(p.Online)
		o.Facts["active_reported"] = strconv.FormatBool(p.Active)
		o.Facts["last_seen"] = p.LastSeen.UTC().Format(time.RFC3339)
		o.Detail = "peer telemetry collected; idle is not offline; probe tailnet service separately"
		if p.CurAddr != "" {
			o.Facts["transport_hint"] = "direct_endpoint_reported"
		} else if p.Relay != "" {
			o.Facts["transport_hint"] = "relay_preference_reported"
			o.Facts["relay"] = adapters.SafeFact(p.Relay)
		} else {
			o.Facts["transport_hint"] = "unknown"
		}
		if !p.Online || status.BackendState != "Running" {
			o.Outcome = "unknown"
			o.Code = "peer_or_backend_not_online"
			o.Detail = "peer/backend not reported online; host power state and service reachability unknown"
		}
		return nil
	}
	o.Outcome = "unknown"
	o.Stage = "adapter"
	o.Code = "peer_not_found"
	o.Detail = "peer absent from this observer's status"
	return nil
}
