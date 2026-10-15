// Package model defines the versioned wire contract shared by server, executors
// and terminal clients. Identity is never derived from an IP address.
package model

import "time"

const MaxSnapshotBytes = 8 << 20

// Schemas of what is written and sent. A topology file is schema 1. Batches
// and snapshots are schema 2: observations carry the contract of their
// check, not the revision of the whole topology. A collector still accepts
// a schema 1 batch, whose observations carry the revision, while that
// revision is its own.
const (
	ConfigSchema   = 1
	BatchSchema    = 2
	SnapshotSchema = 2
)
const Version = "0.9.1"

type Config struct {
	Schema    int        `json:"schema"`
	Project   string     `json:"project"`
	Nodes     []Node     `json:"nodes"`
	Endpoints []Endpoint `json:"endpoints"`
	Services  []Service  `json:"services"`
	Observers []Observer `json:"observers"`
	Links     []Link     `json:"links,omitempty"`
	Paths     []Path     `json:"paths"`
	Checks    []Check    `json:"checks"`
}
type Node struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind,omitempty"` // host, vm, container, router, edge, platform, external
	Parent   string    `json:"parent,omitempty"`
	Group    string    `json:"group,omitempty"`
	Provider string    `json:"provider,omitempty"` // who runs it: a cloud or VPS vendor, an edge or platform
	Location *Location `json:"location,omitempty"` // declared placement for maps; never inferred from IP
	Notes    string    `json:"notes,omitempty"`
}
type Location struct {
	Label string  `json:"label,omitempty"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
}
type Endpoint struct {
	ID       string   `json:"id"`
	Node     string   `json:"node"`
	Address  string   `json:"address"`
	Network  string   `json:"network"`            // public, lan, tailnet, loopback, external
	Observer string   `json:"observer,omitempty"` // mandatory execution scope for loopback
	Via      []string `json:"via,omitempty"`      // edge or proxy nodes that answer first, client side first
	Notes    string   `json:"notes,omitempty"`
}
type Service struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Node      string   `json:"node"`
	Runtime   string   `json:"runtime,omitempty"` // how it is deployed: workers, pages, functions, container, …
	Endpoints []string `json:"endpoints,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Runbook   string   `json:"runbook,omitempty"`
	Notes     string   `json:"notes,omitempty"`
}
type Observer struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Node             string `json:"node,omitempty"`
	Context          string `json:"context"` // describes OS/network namespace, not proof of routing
	RouteExpectation string `json:"route_expectation,omitempty"`
	StaleSeconds     int    `json:"stale_seconds"`
}
type Link struct {
	ID        string `json:"id"`
	From      string `json:"from"` // node IDs; directed initiation, not packet loss direction
	To        string `json:"to"`
	Transport string `json:"transport"`
	Notes     string `json:"notes,omitempty"`
}
type Path struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Observer   string   `json:"observer"`
	Nodes      []string `json:"nodes,omitempty"`
	Links      []string `json:"links,omitempty"`
	Proxies    []Proxy  `json:"proxies,omitempty"`    // explicit wire order, never fallback choices
	Candidates []string `json:"candidates,omitempty"` // other paths; not serial hops
	DependsOn  []string `json:"depends_on,omitempty"`
	Opaque     bool     `json:"opaque,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}
type Proxy struct {
	Kind            string `json:"kind"` // http-connect, https-connect, socks5, ssh
	Address         string `json:"address"`
	Node            string `json:"node,omitempty"`
	Username        string `json:"username,omitempty"`
	PasswordEnv     string `json:"password_env,omitempty"`
	ServerName      string `json:"server_name,omitempty"`
	CAFile          string `json:"ca_file,omitempty"`
	SSHUser         string `json:"ssh_user,omitempty"`
	SSHIdentityFile string `json:"ssh_identity_file,omitempty"`
	SSHKnownHosts   string `json:"ssh_known_hosts,omitempty"`
}
type Check struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Observer          string         `json:"observer"`
	Endpoint          string         `json:"endpoint,omitempty"`
	Service           string         `json:"service,omitempty"`
	Path              string         `json:"path,omitempty"`
	Link              string         `json:"link,omitempty"`
	Kind              string         `json:"kind"`             // http,tcp,tls,ssh-banner,dns,udp-echo,icmp,mihomo,tailscale,wireguard,host
	Target            string         `json:"target,omitempty"` // endpoint address unless overridden explicitly
	IntervalSeconds   int            `json:"interval_seconds"`
	TimeoutSeconds    int            `json:"timeout_seconds"`
	StaleSeconds      int            `json:"stale_seconds"`
	FailureThreshold  int            `json:"failure_threshold"`
	RecoveryThreshold int            `json:"recovery_threshold"`
	Disabled          bool           `json:"disabled,omitempty"`
	Maintenance       bool           `json:"maintenance,omitempty"`
	BindIP            string         `json:"bind_ip,omitempty"`
	ResolveIP         string         `json:"resolve_ip,omitempty"` // pin dial address; original Host/SNI preserved
	ServerName        string         `json:"server_name,omitempty"`
	CAFile            string         `json:"ca_file,omitempty"`
	HTTP              HTTPOptions    `json:"http,omitempty"`
	DNS               DNSOptions     `json:"dns,omitempty"`
	Adapter           AdapterOptions `json:"adapter,omitempty"`
	Host              *HostOptions   `json:"host,omitempty"`
	Notes             string         `json:"notes,omitempty"`
}

// HostOptions selects a local filesystem and optional resource assertions.
// Zero thresholds collect without asserting; process names never include args.
type HostOptions struct {
	DiskPath         string  `json:"disk_path,omitempty"`
	MaxCPUPercent    float64 `json:"max_cpu_percent,omitempty"`
	MaxMemoryPercent float64 `json:"max_memory_percent,omitempty"`
	MaxDiskPercent   float64 `json:"max_disk_percent,omitempty"`
	ProcessName      string  `json:"process_name,omitempty"`
	MinProcesses     int     `json:"min_processes,omitempty"`
	MaxProcesses     int     `json:"max_processes,omitempty"`
}
type HTTPOptions struct {
	Method         string            `json:"method,omitempty"`
	Status         []int             `json:"status,omitempty"`
	Contains       string            `json:"contains,omitempty"`
	BodyRegex      string            `json:"body_regex,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"` // response assertions, not request secrets
	RequestHeaders map[string]string `json:"request_headers,omitempty"`
	BearerEnv      string            `json:"bearer_env,omitempty"`
	ExpectedExit   []string          `json:"expected_exit,omitempty"` // exact IPs or CIDRs
	ExitFormat     string            `json:"exit_format,omitempty"`   // plain, trace, json
	ExitJSONKey    string            `json:"exit_json_key,omitempty"`
	MaxBodyBytes   int64             `json:"max_body_bytes,omitempty"`
}
type DNSOptions struct {
	Name        string   `json:"name,omitempty"`
	Type        string   `json:"type,omitempty"`
	ExpectedIPs []string `json:"expected_ips,omitempty"`
}
type AdapterOptions struct {
	TokenEnv          string              `json:"token_env,omitempty"`
	RootProxy         string              `json:"root_proxy,omitempty"`
	ExpectedMode      string              `json:"expected_mode,omitempty"`
	ExpectedProviders map[string][]string `json:"expected_providers,omitempty"`
	Peer              string              `json:"peer,omitempty"`
	Interface         string              `json:"interface,omitempty"`
}

// Observation is a single bounded measurement. ReceivedAt is assigned by the
// collector; SampledAt must not be replaced when replaying an outbox.
// Contract identifies what its check measured (see config.Contract); a
// schema 1 observation carries the revision of the whole topology instead.
type Observation struct {
	ID           string            `json:"id"`
	Project      string            `json:"project"`
	Contract     string            `json:"contract,omitempty"`
	Revision     string            `json:"revision,omitempty"`
	Check        string            `json:"check"`
	Observer     string            `json:"observer"`
	Boot         string            `json:"boot"`
	Sequence     uint64            `json:"sequence"`
	SampledAt    time.Time         `json:"sampled_at"`
	ReceivedAt   time.Time         `json:"received_at"`
	Outcome      string            `json:"outcome"`  // ok,fail,unknown,error,policy
	Evidence     string            `json:"evidence"` // measured,reported
	Stage        string            `json:"stage"`
	Code         string            `json:"code,omitempty"` // bounded diagnostic, never arbitrary raw error
	Detail       string            `json:"detail,omitempty"`
	DurationMS   int64             `json:"duration_ms"`
	Timings      map[string]int64  `json:"timings,omitempty"`
	Connectivity bool              `json:"connectivity"`
	PathEvidence string            `json:"path_evidence,omitempty"`
	Facts        map[string]string `json:"facts,omitempty"`
}

// Heartbeat says an executor is alive. Contracts digests the contracts of
// its observer's checks as it runs them (see config.ObserverContracts).
type Heartbeat struct {
	Project    string    `json:"project,omitempty"`
	Revision   string    `json:"revision,omitempty"`
	Contracts  string    `json:"contracts,omitempty"`
	Observer   string    `json:"observer"`
	Boot       string    `json:"boot"`
	SampledAt  time.Time `json:"sampled_at"`
	ReceivedAt time.Time `json:"received_at"`
}

// Batch is what an executor delivers. Revision is the topology revision it
// ran; under schema 2 it only describes the sender, whose observations each
// carry their own contract.
type Batch struct {
	Schema       int           `json:"schema,omitempty"`
	Project      string        `json:"project"`
	Revision     string        `json:"revision"`
	Observations []Observation `json:"observations"`
	Heartbeat    *Heartbeat    `json:"heartbeat,omitempty"`
}
type CheckState struct {
	ID        string       `json:"id"`
	Contract  string       `json:"contract"` // the public contract its samples count under
	Observer  string       `json:"observer"`
	Health    string       `json:"health"`    // up,degraded,down,unknown
	Freshness string       `json:"freshness"` // fresh,stale,never,paused,maintenance
	Evidence  string       `json:"evidence"`
	Reason    string       `json:"reason"`
	Failures  int          `json:"failures"`
	Successes int          `json:"successes"`
	Since     time.Time    `json:"since"`
	Last      *Observation `json:"last,omitempty"`
	// Availability is how the check fared over the last day, week and
	// month, from the collector's rollups of its samples.
	Availability []Availability `json:"availability,omitempty"`
}

// Rollup sums what a check's samples said over a span of time. Each
// sample's outcome stands from when it was taken until the next sample or
// until it goes stale, whichever is first: Up while it passed, Degraded
// while it found a policy violated, Down while it failed, and Unknown
// while the monitor could not tell (a monitor-side error). Unmeasured is
// the rest of the span, when no current sample stood for it; it is never
// up or down. Samples and Outcomes count the samples taken in the span,
// and Latency is what the passing ones took.
type Rollup struct {
	Start      time.Time `json:"start"`
	Seconds    int64     `json:"seconds"`
	Up         int64     `json:"up_seconds"`
	Degraded   int64     `json:"degraded_seconds"`
	Down       int64     `json:"down_seconds"`
	Unknown    int64     `json:"unknown_seconds"`
	Unmeasured int64     `json:"unmeasured_seconds"`
	Samples    int64     `json:"samples"`
	// Unreconciled counts samples with unavailable or ambiguous interval
	// boundaries in buckets touching the window. This can exceed Samples
	// when a partial bucket's counts are omitted; time totals are incomplete.
	Unreconciled int64            `json:"unreconciled_samples,omitempty"`
	Outcomes     map[string]int64 `json:"outcomes,omitempty"`
	Latency      *Latency         `json:"latency,omitempty"`
}

// Measured is how long a rollup's samples said whether the check passed:
// its up, degraded and down time.
func (r Rollup) Measured() int64 { return r.Up + r.Degraded + r.Down }

// Latency is what passing samples took: the median and the 95th
// percentile, estimated from a histogram whose bins are a quarter of a
// doubling wide, and the slowest.
type Latency struct {
	Samples int64 `json:"samples"`
	P50     int64 `json:"p50_ms"`
	P95     int64 `json:"p95_ms"`
	Max     int64 `json:"max_ms"`
}

// Availability is a check's rollup over a window ending when its snapshot
// was generated: "24h", "7d" or "30d". Since is the earliest moment the
// collector's rollups hold for the check, when that is inside the window;
// the time before it counts as unmeasured.
type Availability struct {
	Window string `json:"window"`
	Rollup
	Since time.Time `json:"since,omitzero"`
}
type EntityState struct {
	ID       string   `json:"id"`
	Health   string   `json:"health"`
	Reason   string   `json:"reason"`
	Evidence string   `json:"evidence"`
	Affected bool     `json:"affected"`
	Checks   []string `json:"checks,omitempty"`
}
type Incident struct {
	ID              string    `json:"id"`
	Scope           string    `json:"scope"`
	Summary         string    `json:"summary"`
	Hypothesis      string    `json:"hypothesis,omitempty"`
	Checks          []string  `json:"checks"`
	CounterEvidence []string  `json:"counter_evidence,omitempty"`
	Since           time.Time `json:"since"`
}

// IncidentRecord is an incident as a collector's ledger remembers it: when
// its evidence began failing (Since), when the collector first counted it
// (Opened) and, once it is closed, when and how it ended. State is open,
// recovering (its failed checks pass again, short of their recovery
// threshold) or closed; Ended is recovered, unconfirmed (its evidence
// lapsed or no longer confirms it), paused or changed (what its checks
// measure changed, or they were removed). Previous counts the earlier
// records of the same incident that the ledger holds.
type IncidentRecord struct {
	ID       string    `json:"id"`
	Scope    string    `json:"scope"`
	Summary  string    `json:"summary"`
	Checks   []string  `json:"checks"`
	State    string    `json:"state"`
	Since    time.Time `json:"since"`
	Opened   time.Time `json:"opened"`
	Closed   time.Time `json:"closed,omitzero"`
	Ended    string    `json:"ended,omitempty"`
	Previous int       `json:"previous"`
}

// ObserverState is an executor's liveness. Contracts says whether it last
// reported measuring what the collector counts: current, outdated (its
// samples of changed checks are refused until it reloads), or "" when it
// has not said.
type ObserverState struct {
	ID        string    `json:"id"`
	Health    string    `json:"health"`
	LastSeen  time.Time `json:"last_seen"`
	Reason    string    `json:"reason"`
	Contracts string    `json:"contracts,omitempty"`
}
type Snapshot struct {
	DetailsOmitted bool            `json:"details_omitted,omitempty"`
	Schema         int             `json:"schema"`
	Version        string          `json:"version"`
	Project        string          `json:"project"`
	Instance       string          `json:"instance"`
	Revision       string          `json:"revision"`
	GeneratedAt    time.Time       `json:"generated_at"`
	ViewObserver   string          `json:"view_observer,omitempty"`
	Topology       Config          `json:"topology"`
	Checks         []CheckState    `json:"checks"`
	Nodes          []EntityState   `json:"nodes"`
	Endpoints      []EntityState   `json:"endpoints"`
	Services       []EntityState   `json:"services"`
	Paths          []EntityState   `json:"paths"`
	Links          []EntityState   `json:"links"`
	Observers      []ObserverState `json:"observers"`
	Incidents      []Incident      `json:"incidents"`
	// Ledger is what the collector remembers of the incidents in the view:
	// those open, then those closed in the last day, newest first.
	Ledger []IncidentRecord `json:"ledger,omitempty"`
}
