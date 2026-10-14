# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A check's evidence opens with its recent samples, read from the
  collector's history: a strip of one slot per sample colored by outcome,
  with a grey dot for each interval in which none was taken, a bar for
  what each passing sample took, and their median and 95th-percentile
  times with the median DNS, connect, TLS and first-byte times.
  `topo show checks ID` prints the same strip, and its JSON carries it
  slot by slot as `samples`.
- Collectors keep an incident ledger, `incidents.json` in their data
  directory, judged every two seconds from the evidence of all observers
  whether anyone is looking: an incident opens when it is confirmed,
  recovers while its failed checks pass again short of their recovery
  threshold (a failure then is a relapse, not a new incident), and closes
  as recovered, unconfirmed, paused or changed. Each change is logged.
  Snapshots carry the open records and those closed in the last day as
  `ledger`; the Incidents tab and `topo show incidents` list them after
  the open incidents, one row per occurrence, and an incident's evidence
  says when it opened, how often it happened before and how long it
  lasted. A recovering incident still needs attention.
- Everything the TUI shows can be read from the command line. `topo status`
  prints the Overview (verdict, check and tab counts, what needs attention,
  services and observers) and says it in its exit status: 0 all clear,
  2 something needs attention, 3 nothing current is known, 4 no collector
  answered. `topo show TAB [ID]` prints any tab, or the evidence behind
  one entry as the evidence panel has it, a server with its card;
  `topo show map` lists the map's nodes and edges with what each may
  claim. `topo history CHECK` reads a check's recent samples. Each takes
  `--json`, `--observer` and the connection flags of `topo`.
- Collectors and workers reload on `SIGHUP` (`systemctl reload` with the
  shipped units): a collector rereads its topology and `auth.json`, a
  worker its topology. A file that fails validation, names another
  project or drops the process's own observer is refused and logged, and
  the running topology kept; a reload logs which checks measure something
  new and so start afresh. Windows has no `SIGHUP`; restart there.
- Snapshots say, for each observer, whether its executor measures what the
  collector counts. After a collector reload, an observer whose worker
  still runs the old topology reads `outdated`, needs attention and is
  counted on the Observers tab until that worker reloads too. Workers
  report this in their heartbeats.
- Snapshots carry each endpoint's state from the checks that target it,
  and the Topology tab's endpoints show the same evidence lines as other
  entities: the state's reason and the checks behind it.
- `topo preview [--scenario N] status` (or `show`, `history`) reads the
  synthetic evidence, and `topo preview scenarios` lists the scenarios, so
  scripts and agents can be rehearsed on a simulated outage.

### Changed

- An incident starts as early as the ledger saw it fail, rather than at
  the oldest sample a collector still holds for its checks, and in a
  cached view its evidence reads CACHED rather than OPEN.
- **Wire format (schema 2).** Each observation carries the measurement
  contract of its check, a digest of what the check measures (kind,
  observer, target, timeout, bind and resolve settings, assertions,
  adapter options, the proxies of its path), instead of the revision of
  the whole topology. Editing a note, a name, a place, a threshold, an
  interval or another check no longer resets a check's history, refuses
  in-flight samples or renames its incident; changing what it measures
  still starts it afresh. `topo validate` prints every contract, snapshots
  and history show public contracts, and incidents are ordered by scope.
  Collectors accept the schema 1 batches of 0.4 workers while the
  topology is unchanged; upgrade collectors first, then workers, then
  viewers, which read only schema 2 snapshots. Schema 2 clients ignore
  snapshot fields they do not know, so later additions will not break
  them, and pass over a collector of another release rather than refuse.
- What the TUI shows is derived in one place, `internal/view`, which the
  TUI draws and the CLI prints; a test holds the two to the same rows,
  evidence, verdict and counts across every preview scenario.

### Fixed

- A worker no longer backs off after quarantining a sample a collector
  refused, and returns to batching with the first sample accepted; it
  used to slow to one sample per tick, with growing pauses, until its
  queue was empty.
- Endpoints are judged by the same rule as services and nodes. One whose
  checks are degraded reads degraded rather than unknown on the Topology
  tab and its server's card, and one with a confirmed failure while its
  other checks have no current evidence reads unknown rather than degraded.

## [0.3.0] - 2026-10-14

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
- The Overview's big number is lit from above, a shade of its color for
  each of its five pixel rows, as the About wordmark is.
- On small terminals such as 80x24 the Overview keeps the verdict's big
  number, puts the observers beside it, and when nothing needs attention
  gives the room under a short all-clear to the service matrix.
- The glints on the Overview's map run as smoothly as on the Topology
  tab, and About's globe, once its introduction has played, turns at ten
  frames a second rather than fourteen.
- Times in the evidence read alike: an incident's start, a check's last
  sample and when the collector received it are local time to the second
  (`2026-10-13 10:58:07 PDT`), and an incident's start says how long ago
  it was, rather than mixing in RFC 3339.

### Fixed

- PTY acceptance reconstructs the terminal screen before checking state, so
  incremental redraws correctly verify collector failover. CI fuzz suites use
  an execution budget instead of a short wall-clock deadline.
- Large topology diagrams have bounded layout work and an explicit fallback;
  ordinary dense diagrams retain their routing and ordering quality.
- Notices fade out, and the evidence footer identifies Enter as the back action.
- On narrow terminals the header's project and view no longer run into
  the live indicator: they end in an ellipsis before it. Before the first
  snapshot the header no longer says it was updated "never".

### Security

- Enforce per-source collector connection quotas and absolute connection lifetimes;
  close unauthenticated HTTP/1 connections while preserving authenticated reuse.
- Give viewers a public topology revision that excludes private execution settings,
  including revisions embedded in snapshots and observation history. Workers keep
  the private execution revision contract.

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

[Unreleased]: https://github.com/WhitenWhiten/topophany/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/WhitenWhiten/topophany/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/WhitenWhiten/topophany/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/WhitenWhiten/topophany/releases/tag/v0.1.0
