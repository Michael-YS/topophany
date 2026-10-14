# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- About links to the homepage, topophany.com, and to the source on GitHub,
  written as OSC 8 hyperlinks where the terminal takes them
  (`TOPO_HYPERLINKS=0` or `1` decides outright). On short screens the
  blank rows between its paragraphs close up so the links stay.
- Where there is room, About lists the keys of the mode the UI is in
  (viewer, preview or editor) as keycaps, so `?` doubles as help.
- Tabs count what in them needs a look (failing services, paths and
  checks, open incidents, silent observers), red when anything is down and
  amber otherwise; on narrow terminals the tab's number takes the color.
- The terminal window is named after the verdict, such as
  `✖ 2 incidents · homelab · Topophany` (`✎ topology.json ●` in the
  editor), and its own title is put back on exit.

### Changed

- The TUI writes only the cells that changed since the last frame, not
  every line with a change, so animation on the maps sends a few hundred
  bytes a step instead of tens of kilobytes.
- While connecting or loading a view, the header, the status row, the
  Overview's verdict and the empty map show a turning spinner rather than
  a still `◌`, so a slow collector reads as busy, not stuck.
- Menus, forms and confirmations cast a soft shadow onto the dimmed view.
- On small terminals such as 80x24 the Overview keeps the verdict's big
  number, puts the observers beside it, and when nothing needs attention
  gives the room under a short all-clear to the service matrix.
- The glints on the Overview's map run as smoothly as on the Topology
  tab, and About's globe, once its introduction has played, turns at ten
  frames a second rather than fourteen.

### Fixed

- On narrow terminals the header's project and view no longer run into
  the live indicator: they end in an ellipsis before it. Before the first
  snapshot the header no longer says it was updated "never".

## [0.2.0] - 2026-10-13

### Added

- A virtual geo map, now the default when any node has a location: places
  become cards in columns from west to east and north to south, keeping
  the world's order but not its distances, so crowded sites stay legible.
  Each node has its own row, links are wires between rows (and brackets
  inside a card for links within one place), and unplaced nodes such as
  edges and the internet float as pills beside what they connect to. `m`
  cycles virtual geo, geo and network; cards fold on short maps, and links
  that find no room are counted in the legend.
- A Servers tab (`3`): a card for each server with its state, kind,
  provider and place, the services it runs and the edges in front of them,
  its addresses by network, what it hosts, who observes from it, its links,
  and its notes as a memo. Edge and platform cards list what runs there,
  what they stand in front of and where they answered from. Later tabs move
  up one: Services is `4` and About is `9`.
- Edges and platforms: nodes of kind `edge` (Cloudflare, Vercel) and
  `platform` (Supabase) with a `provider`, services with a `runtime`
  (`workers`, `pages`, `functions`, …), and endpoints that name the edges in
  front of them in `via`. Evidence is attributed along `via`: an edge's
  answer, even a 521, proves the edge; the origin counts only a passing
  response not served from the edge's cache.
- HTTP probes record which edge answered, from where and whether from its
  cache (`edge_provider`, `edge_pop`, `edge_region`, `edge_cache`).
- The preview topology puts Cloudflare in front of the main site, with a
  Worker, a Pages site and a Vercel app on Supabase, and gains two
  scenarios: the origin down behind Cloudflare, and the edge unreachable
  from the mainland.

### Changed

- The About globe reads as a sphere instead of a flat disc: a shaded ocean
  and land lit from the upper left, falling off across a soft terminator and
  toward the limb, with a graticule that curves with the surface. Truecolor
  terminals shade under the dots; 256-colour terminals keep a dotted ocean.
  The README banner is redrawn from the same art.
- The homelab example models its CDN as an edge in front of the DMIT
  origin.

### Fixed

- Avoid a data race when reading outbox delivery retry state.
- A service reached over several failing paths no longer repeats "access
  path unavailable" once per path in its reason.

### Security

- Bound Exit-IP and Mihomo JSON structure before decoding; escape bidirectional controls in CLI JSON without changing parsed values.
- Reserve outbox capacity per destination, history capacity per observer, and snapshot computation per viewer; bound connections and pre-authentication work.
- Retain durable observation-ID bindings until administrative archival and scope persisted heartbeats to their project and configuration revision.
- Bind source verification to the tested commit and file manifest; resolve release tags back to the built commit immediately before publication.
- Add regression tests for all eleven scan-bundle findings, including restart recovery, invalid acknowledgements, and moved release tags.

## [0.1.0] - 2026-10-13

Initial release.

### Changed

- The terminal UI is redesigned to be read at a glance: an Overview dashboard
  (verdict, failure count, map, what needs attention, services, observers);
  aligned tables whose state column lights up only problems; a geo map that
  clusters sites by place, colors each arc by its evidence and keeps healthy
  links quiet; a network graph drawn as a left-to-right schematic; and a
  responsive About with a one-bit dithered globe. The UI paints its own
  background (`TOPO_BACKGROUND=terminal` keeps the terminal's) and writes only
  the colors that change.
- Loading has its own calm state instead of the offline warning, the editor's
  status row lists what the draft declares, and the footer keeps the essential
  keys when the terminal is narrow.
- The README opens with a banner drawn from the About art (`make banner`) and
  shows the interface in screenshots.
- Go module path is now `github.com/WhitenWhiten/topophany`.
- Contribution guidelines moved to the repository root; added security policy,
  code of conduct, issue/PR templates, Dependabot and release automation.
- Generated verification records are no longer tracked in `docs/verification/`;
  `scripts/verify.py` writes to `.verify-build/verification/` and CI uploads them
  as an artifact. `scripts/package.py` takes `--verification` to locate them.

### Fixed

- Flaky DNS TCP-fallback test fixture when the paired TCP port was already in use.

### Security

- Isolated API resource budgets, bounded JSON and snapshot decoding, and hardened
  configuration, probe, storage and worker outbox boundaries.
- Release archives are built from an immutable checkout and verified before
  publication; executable GitHub Actions are pinned to commit SHAs.

### Added

- Single `topo` binary: remote TUI client, ingestion collector (`serve`) and
  scoped observation worker (`worker`).
- Native HTTP/TLS, DNS, TCP, UDP and ICMP probes; CONNECT/SOCKS5 proxy chains;
  adapters for local proxy and tunnel tooling.
- Single-writer append journal with checkpoints, crash recovery and
  deduplication; durable worker outbox.
- Evidence- and freshness-based state engine with dependency derivation.
- Terminal UI with geographic and network topology maps, offline preview
  scenarios and a local topology editor.

[Unreleased]: https://github.com/WhitenWhiten/topophany/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/WhitenWhiten/topophany/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/WhitenWhiten/topophany/releases/tag/v0.1.0
