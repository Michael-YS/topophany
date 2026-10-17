package adapters

import (
	"net/url"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func SafeFact(s string) string {
	s = secure.Text(s)
	if len(s) > 2048 {
		n := 2045
		for !utf8.RuneStart(s[n]) {
			n--
		}
		s = s[:n] + "…"
	}
	return s
}
func PublicURL(s string) string {
	if _, e := url.Parse(s); e != nil {
		return ""
	}
	return SafeFact(secure.PublicURL(s))
}
