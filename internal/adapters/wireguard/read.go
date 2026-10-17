package wireguard

import (
	"context"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters"
)

// Query specific public fields. Never request dump, private-key or preshared-keys.
func query(ctx context.Context, command adapters.Command, iface, field string, columns int) (map[string][]string, error) {
	raw, err := command(ctx, "wg", "show", iface, field)
	if err != nil {
		return nil, adapters.CommandFailure(err, "wireguard_unavailable_or_permission_denied")
	}
	return parseRows(raw, columns)
}
func parseRows(raw []byte, columns int) (map[string][]string, error) {
	if len(raw) > 2<<20 {
		return nil, adapters.Local("adapter", "response_limit_exceeded")
	}
	rows := map[string][]string{}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != columns || fields[0] == "" || strings.ContainsAny(fields[0], " \r\x00") {
			return nil, adapters.Local("adapter", "unsupported_wireguard_response")
		}
		if _, exists := rows[fields[0]]; exists {
			return nil, adapters.Local("adapter", "unsupported_wireguard_response")
		}
		for _, value := range fields[1:] {
			if value == "" || strings.ContainsAny(value, "\r\x00") {
				return nil, adapters.Local("adapter", "unsupported_wireguard_response")
			}
		}
		rows[fields[0]] = fields[1:]
		if len(rows) > 4096 {
			return nil, adapters.Local("adapter", "response_complexity_exceeded")
		}
	}
	return rows, nil
}
