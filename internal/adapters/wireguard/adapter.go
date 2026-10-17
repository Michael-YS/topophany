// Package wireguard collects read-only local runtime telemetry.
package wireguard

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func Collect(ctx context.Context, command adapters.Command, options model.WireGuardOptions, o *model.Observation) error {
	b, e := command(ctx, "wg", "show", options.Interface, "dump")
	if e != nil {
		return adapters.Local("adapter", "wireguard_unavailable_or_permission_denied")
	}
	if len(b) > 2<<20 {
		return adapters.Local("adapter", "response_limit_exceeded")
	}
	// The first line includes the interface PRIVATE KEY. It is intentionally
	// discarded; peer preshared keys (column 2) are never retained either.
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 1 || len(strings.Fields(lines[0])) < 4 {
		return adapters.Local("adapter", "unsupported_wireguard_response")
	}
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) != 8 {
			continue
		}
		if f[0] != options.Peer {
			continue
		}
		hs, e := strconv.ParseInt(f[4], 10, 64)
		if e != nil {
			return adapters.Local("adapter", "invalid_handshake_timestamp")
		}
		rx, e := strconv.ParseUint(f[5], 10, 64)
		if e != nil {
			return adapters.Local("adapter", "invalid_transfer_counter")
		}
		tx, e := strconv.ParseUint(f[6], 10, 64)
		if e != nil {
			return adapters.Local("adapter", "invalid_transfer_counter")
		}
		o.Outcome = "ok"
		o.Stage = "adapter"
		o.PathEvidence = "interface_local_runtime_reported"
		o.Facts["rx_bytes"] = strconv.FormatUint(rx, 10)
		o.Facts["tx_bytes"] = strconv.FormatUint(tx, 10)
		if hs > 0 {
			o.Facts["last_handshake"] = time.Unix(hs, 0).UTC().Format(time.RFC3339)
		}
		o.Detail = "peer counters collected; an old handshake alone is not a tunnel failure; probe an inner endpoint"
		return nil
	}
	o.Outcome = "unknown"
	o.Stage = "adapter"
	o.Code = "peer_not_found"
	o.Detail = "peer not present on the configured local interface"
	return nil
}
