# Cloudflare locations capture

`cloudflare-locations-2026-10-19.json` is the unmodified JSON array returned by
`https://speed.cloudflare.com/locations` on 2026-10-19 (309 entries, 30,485
bytes, SHA-256
`6fd21425b338fc8ff37da26f1e1031bf528c537dc695db59e3e24dab64d898b2`). It holds
no secret: the fields are `iata`, `lat`, `lon`, `cca2`, `region` and `city`.

The endpoint answers only to a browser-like client; curl's default user agent
receives `{}`. It was fetched with:

```sh
curl -sS -H 'Referer: https://speed.cloudflare.com/' \
  -H 'Accept: application/json, text/plain, */*' \
  -A 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/140 Safari/537.36' \
  https://speed.cloudflare.com/locations
```

`scripts/gen_colos.py --input` regenerates `internal/view/colos.go` from this
file offline and byte for byte; `--check` verifies the checked-in table without
writing it. Three Indian locations in the list (AIP, AGR and LUH) carry an empty
city, so the generated table keeps them without one.
