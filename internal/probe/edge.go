package probe

import (
	"net/http"

	"github.com/WhitenWhiten/topophany/internal/adapters/cloudflare"
	"github.com/WhitenWhiten/topophany/internal/adapters/cloudfront"
	"github.com/WhitenWhiten/topophany/internal/adapters/fastly"
	"github.com/WhitenWhiten/topophany/internal/adapters/netlify"
	"github.com/WhitenWhiten/topophany/internal/adapters/vercel"
)

// EdgeFacts reads which edge network answered an HTTP request from the
// headers it adds to every response: its name, the point of presence that
// served the request, the function region behind it where one is named,
// and whether its cache answered without asking the origin, in one of
// the words edges have for that. They are what the response says, not
// verified routing: an origin can send them too.
func EdgeFacts(h http.Header) map[string]string {
	// Preserve first-match precedence even when headers name several networks.
	for _, parse := range [...]func(http.Header) (map[string]string, bool){
		cloudflare.HeaderFacts,
		vercel.HeaderFacts,
		cloudfront.HeaderFacts,
		fastly.HeaderFacts,
		netlify.HeaderFacts,
	} {
		if facts, matched := parse(h); matched {
			return facts
		}
	}
	return map[string]string{}
}
