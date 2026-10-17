package wireguard

import (
	"context"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func checkPolicy(ctx context.Context, command adapters.Command, options model.WireGuardOptions, o *model.Observation) error {
	if len(options.ExpectedAllowedIPs) > 0 {
		rows, err := query(ctx, command, options.Interface, "allowed-ips", 2)
		if err != nil {
			return err
		}
		row, exists := rows[options.Peer]
		if !exists {
			return changed(o)
		}
		actual := []string{}
		if row[0] != "(none)" {
			actual = strings.Fields(row[0])
		}
		actualSet, err := prefixSet(actual)
		if err != nil {
			return adapters.Local("adapter", "invalid_allowed_ips")
		}
		expected, _ := prefixSet(options.ExpectedAllowedIPs)
		same := len(expected) == len(actualSet)
		for prefix := range expected {
			if !actualSet[prefix] {
				same = false
			}
		}
		o.Facts["allowed_ips_match"] = strconv.FormatBool(same)
		if !same {
			o.Outcome, o.Code = "policy", "allowed_ips_mismatch"
			o.Detail = "peer AllowedIPs differ from declared policy"
		}
	}
	if options.ExpectedKeepaliveSeconds != nil {
		rows, err := query(ctx, command, options.Interface, "persistent-keepalive", 2)
		if err != nil {
			return err
		}
		row, exists := rows[options.Peer]
		if !exists {
			return changed(o)
		}
		value := row[0]
		if value == "off" {
			value = "0"
		}
		actual, err := strconv.ParseUint(value, 10, 16)
		if err != nil {
			return adapters.Local("adapter", "invalid_keepalive_interval")
		}
		same := int(actual) == *options.ExpectedKeepaliveSeconds
		o.Facts["keepalive_seconds"] = strconv.FormatUint(actual, 10)
		o.Facts["keepalive_match"] = strconv.FormatBool(same)
		if !same {
			o.Outcome, o.Code = "policy", "keepalive_mismatch"
			o.Detail = "peer persistent keepalive differs from declared policy"
		}
	}
	return nil
}
func changed(o *model.Observation) error {
	if o.Outcome != "policy" {
		o.Outcome, o.Code = "unknown", "peer_snapshot_changed"
		o.Detail = "peer membership changed between runtime reads"
	}
	return nil
}
