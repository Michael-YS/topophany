package direct

import (
	"github.com/WhitenWhiten/topophany/internal/secure"
	"strings"
	"unicode/utf8"
)

// discoverySecrets owns all session credentials used to judge public object
// identities and labels. Both checks must use the same complete collection.
type discoverySecrets []string

func (secrets discoverySecrets) contains(text string) bool {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			return true
		}
	}
	return false
}

func (secrets discoverySecrets) conflictsID(id string) bool {
	if secrets.contains(id) {
		return true
	}
	// An ID may be used as a fallback label, so it must not itself expose a
	// credential fragment either.
	for _, secret := range secrets {
		if secret != "" && strings.Contains(secret, id) {
			return true
		}
	}
	return false
}

func (secrets discoverySecrets) cleanName(s, id string) string {
	// Match the complete upstream value before any transformation can cut a
	// credential in half. Sanitization can also join text into a credential.
	if secrets.contains(s) {
		return "System " + id
	}
	s = strings.TrimSpace(secure.Text(s))
	if s == "" || secrets.contains(s) {
		return "System " + id
	}
	if len(s) > 128 {
		s = s[:128]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}
