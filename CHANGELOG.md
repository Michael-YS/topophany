# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- TLS and HTTPS checks, and https-connect hops, no longer report a
  healthy service as `tls: timeout` on a path that drops large
  ClientHellos. Built with Go 1.26, a probe offered Go's default key
  exchanges, led by the hybrid post-quantum X25519MLKEM768, whose key
  share alone is over a kilobyte; where a path never delivered that
  ClientHello, TCP connected and the handshake went unanswered until the
  check timed out, while curl and OpenSSL reached the same server. Probes
  now offer only X25519, P-256, P-384 and P-521, as those clients do.
  Workers still offer the hybrid exchange to their collectors.

### Security

- A worker can no longer crowd other observers' alerts out of a webhook
  they share. A webhook's queue held its 64 alerts whoever they were
  from, so while it was down or slow a worker could fill it with
  incidents of its own observer, which it can make at will, and every
  alert from another observer was dropped until it drained. A full queue
  now gives an alert from an observer with fewer waiting the place of the
  newest from the observer with the most, so each keeps its share; a
  drop is logged with the observer it was from.
- A collector's TLS private key is read as its other credentials are.
  `topo serve` handed `--tls-key` to the TLS server to open as it was,
  so a key other users could read, a link or anything but a file was
  served from; now the key must be a regular file, not a link, of mode
  `0600` or stricter on POSIX, or the collector does not start, and it
  says why. Copy a key out of a certificate tool's directory of links,
  such as Let's Encrypt's `live/`, rather than pointing at it.
- The editor no longer writes a terminal control or bidi rune from a
  topology to the terminal. A field's text was shown without them, but
  its cursor, in color, redrew the rune under it as it was, so moving
  through a field of a crafted topology could send its controls one at a
  time. Any such rune now shows as a space, wherever a single rune is
  drawn, and the cursor stands where the text before it is shown rather
  than a column further per control.

## [0.8.0] - 2026-10-14

Everything since 0.3.0. No release was published between them.

### Added

- On the geo map, `p` hides every site plate and draws the arcs dashed,
  leaving the markers and the lines. `p` again brings the plates back.
  The key does nothing on the virtual geo or the network graph.
- Long-term history. Collectors roll every sample they accept up into
  per-check rollups that outlive the raw journal: each sample's outcome
  stands until the next sample or until it goes stale, and the time it
  stood is summed as up, degraded, down or inconclusive (the monitor could
  not tell), with the samples by outcome and a histogram of what the
  passing ones took. Time no current sample stood for is unmeasured,
  never up or down, so availability reads as "95.7% up over 96%
  measured". Rollups are kept five minutes wide for two days, hourly for
  32 days and daily after that, written with each checkpoint and rebuilt
  from the journal, so a crash counts no sample twice. `topo serve
  --rollup-bytes` caps them (32 MiB by default); at the cap a collector
  sheds the rollups of contracts it no longer counts, then the oldest
  days of the observer whose rollups take the most room, and never stops
  ingesting for them.
- Snapshots carry each check's `availability` over the last 24 hours, 7
  days and 30 days. A check's evidence shows them, such as `Last 24h:
  99.2% up over 96% measured · down 11m`, and the Checks tab its last
  day's share up as UP 24H.
- `GET /v1/rollups?check=ID&step=1h&span=7d` lays a check's rollups out
  in slots of five minutes, an hour or a day, and `topo history CHECK
  --step 1h [--span 7d]` prints them, slot by slot with a total, or as
  JSON. The preview keeps synthetic rollups as a collector watching all
  along would, so scenarios show in them too.
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
- A collector logs what its store holds when it starts: each observer's
  receipts and bytes against its share, and where its receipts settle at
  the last hour's rate, since a receipt lasts the ingestion window and an
  hour. It warns once when an observer is set to fill its share, and
  when, so its sampling or `--retention` can change before its samples
  are refused.
- Webhook alerts. `topo serve --alerts alerts.json` posts incidents that
  open and close, and observers that fall silent or report again, as
  JSON or in the formats of ntfy, Slack and Discord, filtered by event
  and observer. The file names secrets, so it is private to the
  collector like `auth.json` and reloads with it on `SIGHUP`. Each
  webhook has a bounded queue delivered in order, at least once, with
  retries for failures, 408, 429 and 5xx until an alert has waited a
  day. What waits is kept in the data directory as `alert-queue.json`,
  so a collector that stops or reloads loses none of it, and each
  webhook's sent and dropped alerts are counted there and logged at
  start; logs name webhooks by ID, never by URL.
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
- The preview speaks English, Japanese and Chinese. Names, notes and
  scenarios come from one catalog per language in
  `internal/preview/locales`, through references such as `@node.win`; a
  language is supported when its catalog is there. Each language also has
  its own placed topology, so the home is Seattle, Osaka or Chengdu
  rather than one private network shown in translation. The node IDs,
  the scenarios and the evidence stay the same, and a test holds every
  language to that. `topo preview --lang` picks one, and by default the
  first of `LC_ALL`, `LC_MESSAGES` and `LANG` that is set decides,
  falling back to English. `--print-topology` writes the topology in
  that language, and `topo preview scenarios --json` gives each scenario
  an `id` that is the same in every language.
- The homepage is in English at `/`, Japanese at `/ja/` and Chinese at
  `/zh/`, each linking the others, with a language menu and a hint that
  offers a visitor arriving from elsewhere the page in their browser's
  language. The pages are written by `scripts/build_site.py` from one
  template per page in `site/src` and one catalog per language, and each
  language replays a TUI recording of its own preview. It says what
  Topophany is, and can be found as that: a sitemap naming every
  language's page, `robots.txt`, and schema.org data for the site and
  the software. Headings are lit dot by dot, the docs sit beside the
  install, and the semantics are drawn as the TUI draws them. The page
  is a line or two where it was the manual, and what was there for
  effect is gone.
- The README is translated into Japanese (`README.ja.md`) and Chinese
  (`README.zh.md`); a test keeps their commands, links and sections those
  of the original, and each shows the TUI in its own language.
- `make captures` captures the TUI stills in `docs/assets` from the
  preview, painted by the homepage's terminal renderer: the README's in
  every language, the guide's in English. They now show the current TUI.
- `f` shows the map alone, on the Overview or Topology (from another tab,
  the Topology's), and `f` or `Esc` brings the panels back. The selection
  still moves with the arrow keys and is named with its state along the
  map's foot. With the room, the virtual geo says what runs on each node,
  services with a problem first and in their color, on a line under it
  when its cards have the height, and geo plates name them after each
  node.

### Changed

- The geo map labels a site with a plate, its place over a row for each
  node, set down where it covers the least land and the fewest arcs:
  beside its marker when there is room, otherwise joined to it by a thin
  leader, so a crowded region spreads its labels into the sea around it
  rather than stacking them over each other. A short map shows fewer rows
  and counts the rest, then folds a plate to one line. Labels stay put
  while the selection moves.
- Fitting every site, the geo map folds a stretch of longitude wider than
  60° with no site in it, such as the Pacific between China and
  California, to a narrow seam marked with how many degrees it holds, so
  the coasts on either side get the width instead of an empty ocean. The
  focus and world views are never folded. Its natural height is the
  latitudes its sites span with room for their labels, rather than the
  whole world, and on the Overview it keeps the rest when nothing needs
  attention.
- Labels take the room there is rather than a fixed width: virtual geo
  cards widen to their full names where the gaps can spare it, a network
  chip cut short by its column runs on into free cells to its right, a
  service on the Overview with no cell beside it runs on to the edge, and
  a server card's row keeps what it names whole before what it says on
  the right.
- Server cards name the server once, its state in the pill, and take an
  amber or red header when something on them is affected or down. Their
  sections open with a quiet label and a hairline rather than a bright
  heading, and the frame is the card's edge, with no paper outside it.

- Accepting a batch no longer visits every receipt a collector holds.
  Receipts expire from a queue in the order they were taken, and what
  each observer holds is counted as it changes rather than recounted on
  every write, under the store's lock, twice: with 150,000 receipts held
  a write takes 5.7ms rather than 26ms.
- Deduplication receipts expire an hour after the ingestion window
  (`--retention`), when no sample they could match can be accepted any
  more, and release their capacity. Each worker boot keeps the highest
  expired sequence as a floor, so an expired observation ID stays bound
  and is refused with `409 observation_id_conflict`, across restarts.
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
  Collectors accept the schema 1 batches of 0.3 workers while the
  topology is unchanged; upgrade collectors first, then workers, then
  viewers. 0.8 viewers read only 0.8 collectors. Schema 2 clients ignore
  snapshot fields they do not know, so later additions will not break
  them, and pass over a collector of another release rather than refuse.
- What the TUI shows is derived in one place, `internal/view`, which the
  TUI draws and the CLI prints; a test holds the two to the same rows,
  evidence, verdict and counts across every preview scenario.
- `topo preview` follows the locale, so where none of its languages is
  asked for it now shows the built-in topology in English rather than
  Chinese; `--lang zh` shows it as before.
- The reference lists every setting the TUI reads from the environment:
  `NO_COLOR`, `COLORTERM`, `TOPO_BACKGROUND`, `TOPO_HYPERLINKS`,
  `TOPO_CELL_ASPECT` and the locale variables the preview follows.
- The acceptance checklist names what to confirm on live hardware for
  the changes since 0.3.0, among them an overloaded worker, a check
  flapping past its history, storage near capacity, one collector of a
  pair answering slowly, anonymous load on a public collector, and a
  webhook outage across a restart. They are procedures, not results.

### Fixed

- Anonymous requests can no longer lock workers and viewers out of a
  collector. Every request, authenticated or not, was charged to one
  admission of 16 simultaneous requests and 300 a second before its token
  was read, so anyone who could reach a collector could have its workers'
  samples and its viewers refused with 429. Tokens are now checked first;
  a request that authenticates counts only against its own principal's
  30 a second, on every route, and the shared admission bounds the rest.
- A collector slow to answer no longer delays a worker's delivery to its
  other collector. Each round went to both at once but waited for both,
  so one that took eight seconds to time out, or answered slowly without
  failing and so was never backed off, held every round to the other as
  long. Each collector now has a delivery loop of its own, and a backoff
  counts from when a failed attempt ended rather than when it began. A
  worker with a long backlog picks each round's samples without sorting
  all of it: on 45,000 queued samples a round takes 1.7ms and 170KB
  rather than 24ms and 34MB.
- A confirmed failure stays confirmed until its check recovers, however
  long ago the samples that confirmed it were taken. A check was judged
  from the samples a collector still held, so once those that confirmed
  a failure left the history and the latest 40 samples, a check that had
  flapped ever since, never passing twice in a row, read up, and the
  incident ledger closed its incident as unconfirmed and told webhooks
  so. One healthy backend of two behind round-robin does just that. The
  samples a series lets go now leave what they established, kept for
  every pair of thresholds and written with each checkpoint, so a check
  is judged as though they were all still held, across restarts and
  threshold edits.
- The origin behind an edge is credited only with a passing response the
  edge says it fetched from it (`MISS`, `DYNAMIC`, `BYPASS`, `EXPIRED`,
  `REVALIDATED`, `REFRESHHIT` or `PASS`). Any status but a cache hit used
  to count, as did none, so the origin read up on the strength of
  answers the edge made itself: a Cloudflare Worker that sends no
  subrequest reports no cache status, and CloudFront reports
  `LambdaGeneratedResponse`. Behind a hop that reports no cache status,
  such as a plain reverse proxy, the origin is now unknown until a check
  of its own measures it.
- A worker with more checks due than it has workers runs each in turn,
  the longest overdue first. Every pass gave free workers to the checks
  listed first, so under sustained load, such as an outage with many
  checks waiting out their timeouts, those listed last never ran: with
  eight checks on one worker, five never started. A worker that comes
  free also takes the next check at once rather than at the next tick.
- A `Host` in an HTTP check's `request_headers` is sent. It was accepted
  and then dropped, since Go's client sends the URL's host whatever a
  Host header says, so a check meant for a virtual host measured the
  URL's site. A Host value that is not a host or `host:port`, and a
  header named twice in different cases, are refused.
- A `bind_ip` with an IPv6 zone (`fe80::1%eth0`) binds to it. The zone
  was validated and then lost, leaving the kernel to choose the source,
  so such a check measured from whatever address it picked. A zone that
  names no interface on the observer is now a monitor error,
  `local_bind_interface_unavailable`, and an ICMP check refuses an IPv6
  `bind_ip`, since it sends IPv4 only.
- Collectors no longer stop accepting an observer's samples once it has
  delivered its share of receipts. Receipts were kept until
  administrative archival, so of two observers, one sampling once a
  second in all was refused after about a day and a half, and the
  busiest of the preview topology's five after about eight hours. A collector paused this
  way resumes after upgrading, once its old receipts expire.
- The About globe and the geographic map keep their shape on terminals
  whose character cells are not twice as tall as they are wide; on
  Windows Terminal with a tall line height the globe was an upright oval
  and the continents stood 30% too tall. The TUI reads a cell's shape
  from the pixel size the terminal gives with its rows and columns, or
  asks for it with `CSI 16 t`, and `TOPO_CELL_ASPECT` (a cell's height
  over its width, such as `2.6`) sets it where the terminal says neither.
  Windows Terminal answers the query with a fixed 10×20 cell whatever its
  font, so there it takes the variable.
- The homepage no longer scrolls sideways on phones. On narrow screens the
  panels of its Semantics section were as wide as their widest line, up to
  65 pixels wider than the screen at 320; they now fit, and the edge
  diagram's names wrap.
- A worker no longer backs off after quarantining a sample a collector
  refused, and returns to batching with the first sample accepted; it
  used to slow to one sample per tick, with growing pauses, until its
  queue was empty.
- Endpoints are judged by the same rule as services and nodes. One whose
  checks are degraded reads degraded rather than unknown on the Topology
  tab and its server's card, and one with a confirmed failure while its
  other checks have no current evidence reads unknown rather than degraded.

### Security

- A worker token can no longer make a collector write and fsync its
  journal at will. A heartbeat was written before the store compared it
  with the one it held, so one sent again, or an older one, cost a
  journal write and an fsync under the lock every observer's ingestion
  waits on, 30 times a second per token, while changing nothing. A
  heartbeat that would change nothing is now acknowledged without being
  written.
- A viewer token can no longer make a collector redo work over the whole
  topology with each request. `/v1/config` made the public topology
  twice, and `/v1/history` and `/v1/rollups` the public topology and the
  contract of every check to answer for one, as HEAD requests did too,
  and every worker batch hashed the whole topology. What requests need of
  a topology is now worked out once when it is loaded or reloaded.
- A monitored service or Mihomo controller that is sent a secret can no
  longer hand it to viewers. Labels it answered with were kept in the
  observation every viewer reads: the edge headers an HTTP check reads
  its point of presence, region and cache status from, the exit address,
  and a controller's mode, proxy names and test URL. When a request
  carries a secret (a bearer, a private request header, a Mihomo token,
  or a proxy password for a plain HTTP target), the observation now keeps
  closed words, numbers and comparisons with the topology only: no
  `edge_pop`, `edge_region` or `exit_ip`, a `mode` of `rule`, `global`,
  `direct`, `script` or `other`, proxy names the topology declares and
  `undeclared` for the rest, no `test_url`, and `peer_text:
  withheld_secret_request`. Every check's `edge_cache` is now one of the
  statuses edges report, or absent.
- One worker token can no longer shorten another observer's long-term
  history. Rollups past the cap were shed oldest bucket first across
  every check, so an observer sending all it may could push out the
  oldest rollups of observers that held far less. A collector now sheds
  the oldest buckets of the observer whose rollups take the most room;
  each series records its observer, and keeps it through checkpoints.
- Viewers no longer see the fragment of an endpoint address or check
  target (`https://host/path#secret`), nor the user information or query
  of an address that is not a URL (`user:pass@host:22`). The public
  topology removed user information and queries only from URLs with a
  scheme, and kept every fragment. Adapter test URLs and the topology
  now share one sanitizer.
- An SSH hop's `ssh_identity_file` or `ssh_known_hosts` beginning with
  `~` is refused, as the reference already said, rather than read as a
  directory named `~`. The reference now says that a relative path is
  resolved against the worker's working directory, the security notes
  that `tailscale`, `wg` and `ssh` are found on `PATH` rather than at
  fixed paths, `SECURITY.md` that 0.8.x is the supported line, and
  `--outbox-bytes` that quarantine has a budget of its own.

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

[Unreleased]: https://github.com/WhitenWhiten/topophany/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/WhitenWhiten/topophany/compare/v0.3.0...v0.8.0
[0.3.0]: https://github.com/WhitenWhiten/topophany/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/WhitenWhiten/topophany/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/WhitenWhiten/topophany/releases/tag/v0.1.0
