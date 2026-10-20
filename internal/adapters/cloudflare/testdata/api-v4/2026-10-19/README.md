# Synthetic Cloudflare API v4 fixtures — 2026-10-19

**These files are synthetic.** No Cloudflare account, token, tunnel, zone,
pool or load balancer was used to produce them, and no response from a live
account is reproduced here. Every identifier, version, timestamp, address and
content value is made up; the addresses come from the documentation ranges
reserved by RFC 5737 (`192.0.2.0/24`, `198.51.100.0/24`) and RFC 3849
(`2001:db8::/32`), so none belongs to an origin, a person or an account.
`failure_reason`, `comment` and tunnel names are placeholders. No real record
content, origin IP, undeclared origin name or credential is present.

The files exist to pin the response shapes this adapter parses. They were
written from Cloudflare's API reference on 2026-10-19 (fetched over HTTPS with
a browser user agent), not captured from the API:

| File | Fields taken from |
|---|---|
| `tunnel-healthy.json` | [Get a Cloudflare Tunnel](https://developers.cloudflare.com/api/resources/zero_trust/subresources/tunnels/subresources/cloudflared/methods/get/): `success`, `result.status` (`healthy`, `degraded`, `down`, `inactive`), `result.connections[]` with `client_version`, `colo_name`, `is_pending_reconnect`, `opened_at`, and, deliberately present but never read, `origin_ip`, `id`, `client_id`, `uuid`, `account_tag`, `name`. The connection array is documented as deprecated in favor of `/accounts/{account_id}/{tunnel_type}/{tunnel_id}/connections`; see the adapter README. |
| `dns-records.json` | [List DNS Records](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/list/): `success`, `result[]` with `proxied`, `ttl`, `content`, `name`, `type`, `id`, and `result_info`. `content` is compared with the declared value in memory only; it is never stored in an observation. |
| `pool-health.json` | [Pool Health Details](https://developers.cloudflare.com/api/resources/load_balancers/subresources/pools/subresources/health/): `success`, `result.pool_id`, `result.pop_health` with per-region `healthy` and `origins`. The reference renders `pop_health` both as a single object `{healthy, origins}` and as "a list of regions and associated health status"; the generated model collapses the region map. Both renderings are represented (`pool-health.json` uses region keys, `pool-health-single.json` the single object), because the documentation does not settle which one a live response uses; the region-keyed file is the speculative one, while the single-object file matches the generated model. Origin entries are objects keyed by origin address, as in the reference example. |
| — (not fetched as a read) | [Verify Token](https://developers.cloudflare.com/api/resources/user/subresources/tokens/methods/verify/) was read to confirm the token model (`result.status` is `active`, `disabled` or `expired`). The adapter does not call it: each check makes exactly one GET, and a 401 or 403 on the selected read proves the same thing without a second request and without the additional `User API Tokens Read` permission it would need. |

The permission names used in the documentation were verified at
[API token permissions](https://developers.cloudflare.com/fundamentals/api/reference/permissions/):
`Cloudflare Tunnel Read`, `DNS Read` and
`Load Balancing: Monitors and Pools Read`. The page
`https://developers.cloudflare.com/fundamentals/api/reference/tokens/`, which
older links give for token scopes, returned `404 Page Not Found` when fetched
on 2026-10-19; the permissions page
and the token-creation guide
([Create API token](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/))
are the current pages.

## Limits of this provenance

The fixtures verify field names and value vocabularies only. They cannot show
whether a live list of DNS records returns more than one record for a name and
type, whether a live pool report is keyed by region or by colo, whether the
deprecated tunnel `connections` array is still populated, or what Cloudflare's
error bodies look like. The adapter handles each of those conservatively and
the acceptance record names them as unverified. Do not treat a passing test on
these files as a claim about a live account.

Run `go test ./internal/adapters/cloudflare` from the repository root. Tests
serve these bodies from loopback servers only; they never contact
`api.cloudflare.com` and need no token.
