# Beszel direct current-state reads

Direct connects the `topo` process to a Beszel Hub. It needs no Topophany collector,
daemon, metrics service, listening port or database. CPU, memory and the adapter's
primary disk are resource telemetry; a successful Hub read does not prove machine,
application or network reachability. The existing client and preview modes remain
independent. `once` still emits executor observations.

The supported resource mapping is Beszel v0.21.0 plus the four-integer `mem`
schema documented in the [adapter guide](../internal/adapters/beszel/README.md).
Authentication uses ordinary PocketBase `users` password/refresh endpoints, as
specified in the [Records API](https://pocketbase.io/docs/api-records/#auth-refresh).
The new authentication workflow has synthetic loopback coverage. It has not been
validated by logging into a user's Hub; arbitrary Beszel/PocketBase versions,
SSO and complete MFA flows are not promised. Recognized MFA requires a token.

## Configure through stdin

`topo sources list --json` lists direct profiles without contacting the network.
It is separate from `topo monitor stream --profiles`, which continues to list
collector client profiles only. The private root defaults to
`os.UserConfigDir()/topophany/direct`; `--direct-root /absolute/path` overrides it
for every direct read/configuration command. Direct never reads `credentials.json`.
Directories are 0700 and private files 0600 on POSIX.

`inspect`, `save`, `remove` take one JSON object through EOF via `--json-stdin`.
Passwords and tokens belong in stdin, never command arguments. These commands
return one JSON envelope: `schema`, `request_id`, `ok`, and either `data` or
`error:{code,message,retryable}`. Exit codes are 0 success, 1 business/network/
storage failure, 2 invalid command/JSON/known-field structure. Unknown extension
fields are ignored. Input/output limits are 256 KiB / 1 MiB.

For an advanced environment-based setup, first provision `TOPO_BESZEL_TOKEN` in
this process's environment using your own secret workflow, then send:

```sh
topo sources inspect --json-stdin <<'JSON'
{"schema":1,"request_id":"inspect-1","profile":"desktop","source":{"id":"bs-00000000000000000000000000000001","name":"Home Beszel","adapter":"beszel","url":"https://beszel.example.invalid"},"auth":{"mode":"env","name":"TOPO_BESZEL_TOKEN"}}
JSON
```

The URL above is illustrative. Use the discovered **systems.id**, never a server
name, for selection. New clients generate `bs-` plus a UUID's lowercase 32 hex
characters, retaining that ID across save retries. Inspect does not create files,
keep a draft token or save a password. Authentication is checked independently
of whether system discovery returns any systems. A truncated discovery result
is marked `truncated:true`; it is not the complete list.

Save repeats the source and authentication union and adds `base_revision` and
`selected_objects`. First creation uses `base_revision:""`; subsequent edits use
the revision returned by list/save/remove. The selected ID here is synthetic:

```json
{"schema":1,"request_id":"save-1","profile":"desktop","base_revision":"","source":{"id":"bs-00000000000000000000000000000001","name":"Home Beszel","adapter":"beszel","url":"https://beszel.example.invalid"},"auth":{"mode":"env","name":"TOPO_BESZEL_TOKEN"},"selected_objects":["000000000000000"]}
```

Other auth forms are `{mode:"password",identity,password}`, `{mode:"token",token}`
and `{mode:"saved"}`. They are strictly exclusive. Saved requires the same existing
profile/source and Hub URL. Password mode stores the login result, never the
password; list reports auth_mode=password. Env stores only a reference. Replacing
an env token with another principal requires explicit reauthentication/save.

Save upserts one source and preserves other sources. Names come from discovery.
Generated nodes are `source_id + "." + object_id`; the original object ID remains
separate. Renaming does not change identity. Source defaults are 60s collection,
8s timeout, 180s contact and data windows. Generated resource-only bindings set
`max_pages` explicitly and `primary_disk:"root"`; root is the adapter's primary
filesystem ID, not a guessed `/` mount. No alarm thresholds or native active
probes are inserted. Limits: 64 sources/profile, 20 objects/source, 256 objects/
profile, 128 template rows; existing smaller byte budgets still apply.

Remove takes `{schema:1,request_id,profile,base_revision,source_id}`. It only removes
that local source, never an upstream system; the last removal preserves an empty
profile. A conflict requires list and review before retry, without automatic
overwrite. Stable source IDs also make an uncertain save outcome recoverable:
list first, then retry using the same source ID and the current revision.

Credentials are committed before a configuration can reference them. Failures
before publication preserve the old profile. A storage error after an uncertain
filesystem replacement must be reconciled by list; a potentially referenced
credential is retained. Renewals coordinate per credential reference across
processes, then check the current reference and version under the commit lock.
A late response cannot restore a removed source or overwrite a newer credential.
Renewal does not change profile_revision or dataset_id. No password is retained
for an expired nonrenewable token.

## Read current resources

```sh
topo --source direct --profile desktop
topo --source direct --profile desktop --json --wait 15s
topo status --source direct --profile desktop --json
topo show servers --source direct --profile desktop --json
topo show servers SOURCE_ID.OBJECT_ID --source direct --profile desktop
```

`--json` retains the standard Snapshot; status and show retain their existing
view shapes and add structured source status. One-shot commands wait for all
first attempts or an overall deadline (default 15s, `--wait 0..5m`), output what
is known including missing/loading resources, and close their own session.
Status exits 0 ready, 2 current resources need attention, 3 no current resources,
4 all selected sources failed contact. Configuration/argument errors exit 1.
`history --source direct` always returns `history_unsupported_in_direct_mode`,
even for an unknown check. The TUI identifies resource-only direct reads and
explains that resource history is unsupported.

Each process independently polls; two processes may see different records because
of timing. They share configuration/credentials but no samples or confirmation
counts. Equal evidence, config and evaluation time give equal judgments.

For file output:

```sh
topo monitor configure desktop --source direct --profile desktop --output /absolute/path/metrics.json
topo monitor run desktop
```

The default direct template uses sources.status as its bar and servers.list.
Custom templates can request server.card and resource.cpu.used_percent,
resource.memory.used_percent, resource.disk.used_percent. File operations
show/preview/path/export keep their previous meanings. File output cannot overlap
the private direct root. An explicit --direct-root on configure is retained in
that local monitor configuration; run can override it for integration tests.

## Stream and freshness

`topo monitor stream --capabilities` advertises `direct.schema=1`, config_api=1,
adapters=[beszel], retry and hard limits. Capability discovery does no filesystem,
credential or Hub I/O. Do not use the binary version as a feature test.

The [D1 specification and fixtures](contracts/direct-v1/contract.md) define exact
request/frame fields. A new valid request immediately emits running + loading.
Failures remain source-scoped data. Invalid requests and revision conflicts keep
the previous request installed. Every explicit request reloads public config;
there is no mandatory file watcher. Template, display interval and observer
changes preserve collection. Changed acquisition settings replace only affected
source loops and reject their late execution generations.

Source polling is independent of the 5s default display cadence. Desktop transient
backoff is approximately 5/10/20/40/60s, capped and jittered; collector/worker
backoff is unchanged. retry_nonce triggers an explicit coalesced retry with a 5s
per-source debounce and never overrides Retry-After. The session retains 1024
nonce identities; further new values are rejected with state_budget rather than
evicting old identities and retrying a replay. Permanent authentication and
permission failures wait for explicit retry or new authentication. A refreshable
401 permits one renewal/read retry; another 401 stops that cycle.

A record's sample time is its Hub `created`, not receipt, contact or publication.
Repeated reads can renew successful Hub contact but cannot renew that sample.
Counts partition selected objects into current/stale/missing. `attention` counts
stale+missing servers, not incidents. Origin identifies the selected resource's
actual adapter/source/object; Beszel is not the machine's provider. Unknown
provider/location remains unknown. Empty profiles are no_data, never ready.

Bar widgets must label direct data as an as-of snapshot and expire at the first
relevant row/resource deadline. The previous 30m+15m widget lease cannot extend
resource validity. Tokens never belong in stream frames or App Group handoffs.

## Synthetic acceptance Hub

Build and test with isolated directories:

```sh
make build
python3 scripts/direct-contract.py --topo "$PWD/bin/topo" --out "$(mktemp -d /tmp/topophany-direct.XXXXXX)"
python3 scripts/direct-contract.py --serve-fixture-hub --ready-file /absolute/new/temp/ready.json
```

The latter binds literal 127.0.0.1 and runs until SIGINT/SIGTERM. Its new 0600
ready file contains only the synthetic URL, login, token and system IDs. Options:
`--objects 0..1001`; `--scenario ready|missing|stale|auth_required|forbidden|rate_limited|unavailable|mfa`;
`--delay-seconds 0..60` simulates timeout. Rate limit uses Retry-After:60; stale
returns a fixed record already 600s old; missing returns a successful empty stats
list. Ordinary fixture records stay unchanged after startup, allowing expiry
checks with a short max_age_seconds. Restart with ready to exercise reconnect.
Canonical serialization fixtures use the separate fixed clock
2026-10-21T12:00:00Z and synthetic identifiers. Live harness timing is relative to
its startup. See [HANDOFF](contracts/direct-v1/HANDOFF.md) for actual test evidence.
