# Contributing

Keep one binary and one versioned model. Preserve the separation between source,
requested route, runtime-reported choice and actual response evidence. Never
turn a monitor-side permission/API error into a target outage.

Run `go fmt ./...`, `go vet ./...`, `go test -race ./...` and the complete
`scripts/verify.py` suite before publishing a release. Add a named regression
case to [SCENARIOS.md](docs/SCENARIOS.md) for every semantic bug. Tests must use temporary
files, local listeners and synthetic credentials. A mocked adapter must never
be described as a production deployment test.

Use bounded contexts/reads/queues, do not log raw upstream errors or secrets,
and keep configuration changes explicit. Adding an outbound protocol must not
introduce silent direct fallback. Adding an API GET endpoint still requires a
side-effect review. Do not add remote execution permissions to viewer/worker
roles. Keep the original observation timestamp during replay.

No third-party runtime dependencies are currently required. A future storage
or TUI library change must retain the wire contract, migration/recovery tests,
cross-builds and resource-budget behavior. JSON topology is the sole writable
configuration authority in version 0.2.

The homepage at topophany.com is the static site in `site/public/`, published
as is by a Cloudflare Worker (`site/wrangler.jsonc`) with no build step, and
redeployed by Workers Builds on every push to `main` that touches `site/`;
`site/go.mod` keeps it out of the Go module. Its pages are written, one per
language, from the templates in `site/src/` with `{{key}}` where words go and
the words of each language in `site/src/locales/<language>.json`; edit those,
never the pages in `site/public/`. Its styles and scripts are edited in
`site/src/css/` and `site/src/js/` and published under names that carry a
digest of their content (`site.<digest>.css`), and the data the scripts fetch
is named with its digest as a query, so browsers keep them for good and a page
never runs with older ones (`site/public/_headers`). Its terminal replays
(`site/public/data/tour.<language>.json`) are recorded from `topo preview` by
the TUI's own renderer. Run `make site` after changing the TUI, the templates,
the words, the styles or the scripts to write them again (`python3
scripts/build_site.py` alone when the TUI is unchanged);
`scripts/test_site.py` fails while they are stale.

The TUI stills in `docs/assets` (`<name>.<language>.png`) are captured from the
preview the same way: the README's in every language, the guide's in English.
The README's animations (`<name>.<language>.gif`) are chapters of the
homepage's tour. Run `make captures` after changing what they show, and after
`make site`. It needs a Chromium (found on `PATH`, or given as `CHROME`) and
ffmpeg (found on `PATH`, or given as `FFMPEG`), and Japanese wants a font with
Japanese glyph forms; `make captures FONTS=dir` adds a directory of fonts, such
as Noto Sans JP, for that run only.

Words a reader sees go in catalogs, never in code. The preview's built-in
topology refers to its names, notes and places as `@key`, and each language's
text for them and for the scenarios is in `internal/preview/locales/`. To add a
language, add a catalog there and in `site/src/locales/`, a `README.<language>.md`,
and run `make site` and `make captures`; the tests list what is still missing.
