// Package edgeheaders bounds the reported labels shared by edge adapters.
// Header values are claims made by the response, not verified routing evidence.
package edgeheaders

import "strings"

// SetFact keeps the existing short-label and closed cache-status policy.
func SetFact(out map[string]string, key, value string) {
	if key == "edge_cache" {
		if value = strings.ToUpper(token(value)); !cacheStatuses[value] {
			return
		}
	}
	if value = token(value); value != "" {
		out[key] = value
	}
}

// LastHop selects the last comma-delimited entry, nearest the client.
func LastHop(list string) string {
	parts := strings.Split(list, ",")
	return strings.TrimSpace(parts[len(parts)-1])
}

// CacheWord reads the first word of the last hop's cache report.
func CacheWord(value string) string {
	if fields := strings.Fields(LastHop(value)); len(fields) > 0 {
		return strings.ToUpper(fields[0])
	}
	return ""
}

// Preserve the pre-split allowlist, including its truncated CloudFront label.
var cacheStatuses = map[string]bool{"HIT": true, "MISS": true, "EXPIRED": true, "STALE": true, "BYPASS": true, "REVALIDATED": true, "UPDATING": true, "DYNAMIC": true, "NONE": true, "UNKNOWN": true, "PRERENDER": true, "REFRESHHIT": true, "PASS": true, "ERROR": true, "REDIRECT": true, "LAMBDAGENERATEDRESPONSE": true, "FUNCTIONGENERATEDRESPONS": true}

func token(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if b.Len() >= 24 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
