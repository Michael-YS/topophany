// Package wireguard collects read-only local runtime telemetry.
package wireguard

import (
	"context"
	"strconv"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func Collect(ctx context.Context, command adapters.Command, options model.WireGuardOptions, o *model.Observation) error {
	if Validate(options) != nil {
		return adapters.Local("setup", "invalid_wireguard_options")
	}
	handshakes, err := query(ctx, command, options.Interface, "latest-handshakes", 2)
	if err != nil {
		return err
	}
	transfers, err := query(ctx, command, options.Interface, "transfer", 3)
	if err != nil {
		return err
	}
	hs, hasHS := handshakes[options.Peer]
	counts, hasCounts := transfers[options.Peer]
	o.Facts["sampling_atomicity"] = "separate_command_reads_not_atomic"
	if !hasHS || !hasCounts {
		o.Outcome, o.Stage, o.Code = "unknown", "adapter", "peer_not_found"
		o.Detail = "peer not present on the configured local interface"
		if hasHS != hasCounts {
			o.Code = "peer_snapshot_changed"
			o.Detail = "peer membership changed between runtime reads"
		}
		return nil
	}
	seconds, err := strconv.ParseInt(hs[0], 10, 64)
	if err != nil || seconds < 0 || seconds > time.Now().Add(30*time.Second).Unix() {
		return adapters.Local("adapter", "invalid_handshake_timestamp")
	}
	rx, e1 := strconv.ParseUint(counts[0], 10, 64)
	tx, e2 := strconv.ParseUint(counts[1], 10, 64)
	if e1 != nil || e2 != nil {
		return adapters.Local("adapter", "invalid_transfer_counter")
	}
	o.Outcome, o.Stage = "ok", "adapter"
	o.PathEvidence = "interface_local_runtime_reported"
	o.Facts["rx_bytes"], o.Facts["tx_bytes"] = strconv.FormatUint(rx, 10), strconv.FormatUint(tx, 10)
	if seconds > 0 {
		o.Facts["last_handshake"] = time.Unix(seconds, 0).UTC().Format(time.RFC3339)
	}
	o.Detail = "peer counters collected; an old handshake alone is not a tunnel failure; probe an inner endpoint"
	return checkPolicy(ctx, command, options, o)
}
