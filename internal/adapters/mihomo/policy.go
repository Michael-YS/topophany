package mihomo

import (
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// closedMode is a Mihomo mode as one of the words Mihomo has for it.
func closedMode(s string) string {
	switch s = strings.ToLower(s); s {
	case "rule", "global", "direct", "script":
		return s
	}
	return "other"
}

// declared are the proxy names an adapter check's topology gives, which
// its viewers already see, and the two Mihomo builds in.
func declared(a model.MihomoOptions) map[string]bool {
	known := map[string]bool{"DIRECT": true, "REJECT": true}
	if a.RootProxy != "" {
		known[a.RootProxy] = true
	}
	for _, names := range a.ExpectedProviders {
		for _, n := range names {
			known[n] = true
		}
	}
	return known
}
