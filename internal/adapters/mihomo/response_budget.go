package mihomo

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

const maxCoreProxies = 4096

// Only inventory sections get larger budgets. An ignored field cannot borrow
// their allowance. Check representation cost and duplicate keys before typed
// decoding, including fields the adapter will not otherwise consume.
func coreResponseBudget(raw []byte, endpoint string) string {
	if endpoint == "/configs" {
		if secure.JSONResponseBudget(raw, 128, 256, 4096) != nil {
			return "response_complexity_exceeded"
		}
		return ""
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tokens, containers, entries := 0, 0, 0
	var value func([]string) string
	value = func(path []string) string {
		tokens++
		if len(path) > 32 || tokens > 1000000 {
			return "response_complexity_exceeded"
		}
		t, err := d.Token()
		if err != nil {
			return "response_complexity_exceeded"
		}
		if s, ok := t.(string); ok && len(s) > 4096 {
			return "response_complexity_exceeded"
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return ""
		}
		containers++
		if containers > 65536 || delim != '{' && delim != '[' {
			return "response_complexity_exceeded"
		}
		limit, code := 128, "response_complexity_exceeded"
		if delim == '[' {
			limit = 256
		}
		if endpoint == "/proxies" && len(path) == 1 && path[0] == "proxies" && delim == '{' {
			limit, code = maxCoreProxies, "proxy_inventory_capacity_exceeded"
		}
		if delim == '[' && len(path) == 3 {
			if endpoint == "/proxies" && path[0] == "proxies" && path[2] == "all" ||
				endpoint == "/providers/proxies" && path[0] == "providers" && path[2] == "proxies" {
				limit, code = maxCoreProxies, "proxy_members_capacity_exceeded"
			}
		}
		if delim == '[' && endpoint == "/providers/proxies" && len(path) == 5 && path[0] == "providers" && path[2] == "proxies" && path[3] == "[]" && path[4] == "all" {
			limit, code = maxCoreProxies, "proxy_members_capacity_exceeded"
		}
		keys := map[string]bool{}
		count := 0
		for d.More() {
			count++
			if count > limit {
				return code
			}
			key := "[]"
			if delim == '{' {
				k, err := d.Token()
				if err != nil {
					return "response_complexity_exceeded"
				}
				var valid bool
				key, valid = k.(string)
				if !valid || len(key) > 128 || keys[key] {
					return "response_complexity_exceeded"
				}
				keys[key] = true
			} else {
				entries++
				if entries > 262144 {
					return "response_complexity_exceeded"
				}
			}
			if code := value(append(path, key)); code != "" {
				return code
			}
		}
		if _, err := d.Token(); err != nil {
			return "response_complexity_exceeded"
		}
		return ""
	}
	if code := value(nil); code != "" {
		return code
	}
	if _, err := d.Token(); err != io.EOF {
		return "response_complexity_exceeded"
	}
	return ""
}
