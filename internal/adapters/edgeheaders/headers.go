// Package edgeheaders bounds the reported labels shared by edge adapters.
// Header values are claims made by the response, not verified routing evidence.
package edgeheaders

import "strings"

// SetFact keeps the short-label policy and the closed cache and mitigation
// vocabularies.
func SetFact(out map[string]string, key, value string) {
	switch key {
	case "edge_cache":
		if value = strings.ToUpper(token(value)); !cacheStatuses[value] {
			return
		}
	case "edge_mitigation":
		if value = strings.ToLower(strings.TrimSpace(value)); value != "challenge" {
			return
		}
		out[key] = value
		return
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

// CacheStatus reports whether word is one the closed cache vocabulary keeps.
// Rollups count the same words the header parser admits, so the vocabulary
// has one owner.
func CacheStatus(word string) bool { return cacheStatuses[word] }

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
