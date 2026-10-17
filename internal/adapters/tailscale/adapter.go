// Package tailscale collects read-only local runtime telemetry.
package tailscale

import (
	"context"
	"strconv"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func Collect(ctx context.Context, command adapters.Command, options model.TailscaleOptions, o *model.Observation) error {
	b, err := command(ctx, "tailscale", "status", "--json")
	if err != nil {
		return adapters.CommandFailure(err, "tailscale_unavailable_or_permission_denied")
	}
	status, err := parseStatus(b)
	if err != nil {
		return err
	}
	o.Facts["backend_state"] = closedBackend(status.BackendState)
	if len(status.Health) > 0 {
		o.Facts["health_warning_count"] = strconv.Itoa(len(status.Health))
	}
	if status.BackendState != "Running" {
		o.Outcome, o.Stage, o.Code = "unknown", "adapter", backendCode(status.BackendState)
		o.PathEvidence = "local_backend_runtime_reported"
		o.Detail = "local Tailscale backend is not running; peer reachability is unverified"
		return nil
	}
	peer, code := resolvePeer(status.Peer, options.Peer)
	if code != "" {
		o.Outcome, o.Stage, o.Code = "unknown", "adapter", code
		o.Detail = "peer identity could not be resolved uniquely in this observer's status"
		if code == "peer_not_found" {
			o.Detail = "peer absent from this observer's status"
		}
		return nil
	}
	o.Outcome, o.Stage = "ok", "adapter"
	o.PathEvidence = "peer_local_runtime_reported"
	o.Facts["active_reported"] = strconv.FormatBool(peer.Active)
	o.Facts["last_seen"] = peer.LastSeen.UTC().Format(time.RFC3339)
	o.Detail = "peer telemetry collected; idle is not offline; probe tailnet service separately"
	switch {
	case peer.PeerRelay != "":
		o.Facts["transport_hint"] = "peer_relay_reported"
	case peer.CurAddr != "":
		o.Facts["transport_hint"] = "direct_endpoint_reported"
	case peer.Relay != "":
		o.Facts["transport_hint"] = "relay_preference_reported"
		o.Facts["relay"] = adapters.SafeFact(peer.Relay)
	default:
		o.Facts["transport_hint"] = "unknown"
	}
	if peer.Online == nil {
		o.Facts["online_reported"] = "unknown"
		o.Outcome, o.Code = "unknown", "peer_online_unavailable"
		o.Detail = "this response does not report whether the peer is online"
	} else {
		o.Facts["online_reported"] = strconv.FormatBool(*peer.Online)
		if !*peer.Online {
			o.Outcome, o.Code = "unknown", "peer_offline"
			o.Detail = "peer is reported offline; host power state and service reachability are unknown"
		}
	}
	return nil
}
