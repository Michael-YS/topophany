# Security Boundaries

The threat model of Topophany assumes that the network, probe targets, and proxy API responses may be untrusted, while the operating system, installed binaries, audited local configurations, and restricted credential directories are controlled by the administrator. It does not defend against local attackers who have already gained privileges of the same user or root/administrator privileges.

## Credentials and Transport

The browser viewer connects directly to a user-selected collector using a
viewer token held in its worker's memory. The token is cleared from the form
on submission, is not written to URLs, cookies or browser storage, and is
discarded with the worker on disconnect/navigation. Browser TLS trust applies;
there is no certificate bypass. Fetch omits cookies and rejects redirects
before following them. Collector reads retain the native client's project,
schema, body-budget and freshness checks. Only the collector worker's CSP
allows outbound HTTPS (and literal `127.0.0.1` HTTP for local development);
the synthetic demo retains a same-origin connection policy. A collector must
explicitly opt in to a browser origin with `--web-origin`. This permits CORS
reads, never unauthenticated evidence or ingestion, and its preflights remain
subject to the existing anonymous admission budgets. The web origin's scripts
and the user's browser/extensions are part of the trusted client environment.

The collector's `auth.json` stores only SHA-256 digests of 256-bit random bearer tokens. The viewer role is strictly read-only; the worker role can only write results within its scoped observer boundary that match the current project and configuration contract. A worker report remains evidence supplied by a trusted executor rather than hardware remote attestation; stealing a worker token allows spoofing that specific observer perspective, so tokens should be rotated immediately and the collector restarted to reload `auth.json`.

Client and executor tokens use private files or `token_env`. HTTP probe bearer tokens and proxy passwords also reference environment variables; placing plaintext Authorization or Cookie headers directly in request header configurations is prohibited. Note that environment variables can still be read by other processes running under the same user privileges and do not constitute a full secrets management service. Do not place URLs containing tokens in notes, runbooks, screenshots, or commit histories; generic note fields cannot automatically sanitize all secrets.

POSIX credential files, the collector's TLS private key among them, require `0600` or stricter file permissions and reject static symlinks and non-regular files; a collector whose key is otherwise refuses to start. Copy a key out of a certificate tool's directory, such as Let's Encrypt's `live/` whose files are links, rather than pointing `--tls-key` at it. The parent directory must be owned and controlled by a trusted user; no defense is claimed against race-condition replacement attacks within a world-writable parent directory. Windows runtime reads rely on filesystem ACLs. Native bundle generation and installation replace and verify directory and existing child DACLs, allowing only the current user SID and necessary Administrators/SYSTEM before writing credentials. They reject unrelated owners and reparse points; they do not audit arbitrary parent-directory ACLs or defend against replacement races in untrusted writable parents. Manual deployers must confirm those same read-access limits.

Privileged Unix installation checks every existing component of installation
and data paths before mutation and rejects symbolic links, except macOS's fixed
root-owned `/etc`, `/var` and `/tmp` system aliases. Directory creation uses
verified directory handles and data ownership changes use an open descriptor.
If preparation fails, cleanup removes only empty directories created by that
attempt. Root and the administrator invoking sudo remain trusted installers.

Remote server URLs must use HTTPS with valid hostnames and CA verification. There is no `InsecureSkipVerify` bypass. Ambient environment `HTTP_PROXY` is ignored and HTTP redirects are not followed, preventing token forwarding to arbitrary servers. Literal loopback addresses may use HTTP, though API bearer tokens are still required. A malicious local user or a rogue proxy listening on loopback is not a secure substitute for TLS. The `--allow-insecure` flag is an explicit acceptance of plaintext risk, not a recommended production deployment method.

HTTP/Mihomo bearer credentials, custom request headers (including API keys), and a query in an HTTP check's URL, which may carry a service's token, require HTTPS except for direct literal-loopback targets. A non-loopback `resolve_ip` or any proxy path removes the plaintext exception. Credentialed HTTP CONNECT and SOCKS5 are allowed only as the first, literal-loopback hop; use HTTPS CONNECT for remote credentialed hops. TLS or SSH to an earlier proxy does not encrypt a later proxy-to-proxy leg. These checks run both during configuration validation and before execution.

A peer that receives a probe or proxy credential can reflect it in a response.
Secret-bearing HTTP observations therefore retain closed provider/cache words
and assertion outcomes, but omit raw HTTP status codes and other unrestricted
response labels. Mihomo also withholds history delay/time, provider load times,
numeric counts and exact unexpected API statuses under the same credential
boundary. Success, failure, fixed policy comparisons and locally measured timing
remain availability signals; this policy does not eliminate covert channels in
a peer's choice of availability or timing.

Temporary overrides use `--token-stdin` or `--credential-file` (private, regular files only); raw `--credential TOKEN` arguments are rejected. An override requires `--server` when the saved profile contains multiple endpoints. It never replaces credentials across an implicit failover set. Terminal output removes every Unicode `Bidi_Control` character as well as terminal control characters, and so does everything `topo` writes to stderr, its errors and logs, but for newlines and tabs. `--json` output writes each of them, C0, DEL and the C1 controls a terminal may read as escape sequences alike, as a `\u` escape, so it reads back as the collector sent it with none of them literal. A topology value an error refuses is quoted, its controls escaped, so the error shows what is wrong with it.

Ingress diagnostics name the addresses that failed a TLS handshake before
authenticating. Viewer snapshots pseudonymize those addresses with a short
keyed digest by default, because a viewer token is read-only but not a licence
to learn every observer's or scanner's address. The key lives only in the
collector process and changes on restart, so the digest is stable across
snapshots but cannot be reversed by enumerating the IPv4 space offline.
`--expose-ingress-sources` explicitly includes the raw addresses for an
operator who needs them.

## Capability Boundaries

The optional `scripts/serve_site.py` development bridge requires the private
one-time launch link printed in its terminal. The fragment secret expires in
five minutes and is removed from browser history before it is exchanged for a
process-local viewer token. Unauthenticated pages and scripts never contain
either secret. Loopback binding and browser-origin headers alone do not
authenticate a local OS user. Restart the bridge to obtain another launch link;
keep the terminal link private like other viewer credentials.

The API provides no endpoints for modifying topology, altering proxy routing policies, reading arbitrary files, running remote commands, or restarting services. Workers load reviewed checks from local files and do not poll or receive arbitrary tasks to execute from the collector. Because probing arbitrary targets inherently grants access to networks reachable by the executor, the audited configuration file itself forms the authorization boundary; never load untrusted third-party topology files automatically.

The proxy adapter reads only explicitly allowlisted endpoints (such as safe configuration and status queries), forbidding arbitrary GET endpoints from being treated as side-effect-free authorizations. Tunnel tools (`tailscale` and `wg`) and `ssh` for SSH hops are looked up on the process `PATH` and invoked directly, never through a shell, with fixed argument structures. OpenSSH is used solely for `-W` stream forwarding with strict host-key checking and never opens an interactive shell; unknown host keys are rejected and agent forwarding is disabled. Binary installation directories and system PATH must be trusted. Because tunnel interfaces like WireGuard often require elevated privileges, probe in-tunnel services where possible rather than running the entire monitoring process persistently as root.

Raw ICMP sockets require appropriate system permissions. Missing permissions are reported as monitor errors; do not grant broad administrative privileges to the entire process merely to achieve a clean dashboard. Bandwidth and probe budgets are not port-scanning authorizations. Default checks are lightweight and do not offer throughput stress testing or automated network-wide discovery.

All network responses, API requests, historical records, terminal inputs, and outbox queues are bounded. Disabling HTTP redirects and connection reuse carries explicit diagnostic semantics. Probing through a proxy cannot guarantee that every request establishes a brand-new underlying tunnel connection. Standard HTTP GET and HEAD requests may still trigger unintended side effects on poorly implemented endpoints, so health check targets must be reviewed and approved by administrators.

## Storage and Failures

Installer rollback accepts only `.backups/install-*` directories inside the
original installation. A private per-installation key authenticates the record,
its exact backup path, service state and saved-file digests. All saved bytes are
verified before stopping a service or changing targets. POSIX backup directories
and files must belong to the invoking installer; privileged installation and
rollback require root-controlled directory chains, including the installation
directory. A sudo caller's ownership alone is insufficient. Windows verifies
the installation's restricted ACLs and ownership before reading a backup.
Keep `.backups/rollback.key` private and retain it with its installation; copied,
moved, modified and unsigned legacy backups are refused. For legacy recovery,
review and generate an installation bundle rather than importing a rollback
record as a privileged service definition.

Log checksums detect accidental corruption; they are not keyed cryptographic signatures and do not prevent a local attacker from recalculating checksums. Power-loss guarantees for fsync and atomic renames depend on the operating system and the underlying local filesystem; single-process crash recovery tests do not equate to power-loss simulations across all disks, RAID controllers, or virtualization platforms. Directory fsync and ACL semantics on Windows differ from POSIX platforms, and identical Linux power-loss guarantees are not claimed on Windows.

Do not share database or log directories across NFS or SMB network filesystems. The system does not implement automated multi-master cluster management. Stale results, clock skew, and missing sources must be preserved as-is rather than having timestamps rewritten to make dashboards look clean. Log files, quarantine stores, and history buffers may contain internal addresses and service names, and must be protected with the same care as topology files.

History drawn under a snapshot uses that snapshot's collector, public revision
and check contract. A collector changing configuration between these reads
produces unavailable history until the view refreshes. Switching snapshot
collectors clears cached samples and charts; it does not merge independently
retained histories. Standalone `topo history` retains ordered read failover and
reports the source of its own response.

When reporting security issues, provide only sanitized reproduction configurations and observation logs. Do not paste production tokens or sensitive infrastructure details into public issues. Use private vulnerability reporting channels on the hosted repository.
