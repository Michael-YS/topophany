# Vercel

Pure response-header interpretation for `X-Vercel-Id` and `X-Vercel-Cache`.
A nonempty ID selects the provider. The parser accepts `region::id` and
`region1:region2:...::id`, retaining up to eight exact region codes (three
lowercase letters followed by one or two digits) in `vercel_route_regions`.
`vercel_edge_region` is the receiving region, the first in that sequence. The full
header is limited to 1024 bytes. Invalid or oversized location metadata is
omitted while recognized provider/cache facts remain available.

The [official helper](https://github.com/vercel/vercel/blob/main/packages/functions/src/headers.ts)
extracts the first region. Vercel's [request routing explanation](https://vercel.com/blog/life-of-a-vercel-request-navigating-the-edge-network)
states that the request ID excludes PoPs. Topophany therefore emits no
`edge_pop` for Vercel and does not label arbitrary additional hops as function
execution. Old stored location keys are displayed without assigning a role; the receiving region uses a new key so historical function labels are not reinterpreted. Request IDs themselves are never stored as facts. Regions are
reported routing labels and do not establish origin or application health.

The HTTP probe suppresses all region labels for private requests, including
query strings and authenticated requests. Only closed provider/cache words
survive that boundary. Parsing makes no API calls and needs no new config.

Intentional 1.2 differences: old `hnd1::iad1::id` interpretation is rejected;
`hnd1:iad1::id` yields receiving region `hnd1` and ordered routing regions
`hnd1:iad1`. The independent pre-split oracle in `probe/edge_legacy_test.go`
is unchanged. Compatibility checks exempt these location fields only for a
selected Vercel response, preserving every other field and provider precedence.
Golden format, bound, mutation and private-response reflection tests cover the
new behavior. Run `go test ./internal/adapters/vercel ./internal/probe ./internal/view`.
