// Package model defines the versioned wire contract shared by server, executors
// and terminal clients. Identity is never derived from an IP address.
package model

import "time"

const SchemaVersion = 1
const Version = "0.1.0"

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
	Kind     string    `json:"kind,omitempty"`
	Parent   string    `json:"parent,omitempty"`
	Group    string    `json:"group,omitempty"`
	Location *Location `json:"location,omitempty"` // declared placement for maps; never inferred from IP
	Notes    string    `json:"notes,omitempty"`
}
type Location struct {
	Label string  `json:"label,omitempty"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
}
type Endpoint struct {
	ID       string `json:"id"`
	Node     string `json:"node"`
	Address  string `json:"address"`
	Network  string `json:"network"`            // public, lan, tailnet, loopback, external
	Observer string `json:"observer,omitempty"` // mandatory execution scope for loopback
	Notes    string `json:"notes,omitempty"`
}
type Service struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Node      string   `json:"node"`
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
	Kind              string         `json:"kind"`             // http,tcp,tls,ssh-banner,dns,udp-echo,icmp,mihomo,tailscale,wireguard
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
	Notes             string         `json:"notes,omitempty"`
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
type Observation struct {
	ID           string            `json:"id"`
	Project      string            `json:"project"`
	Revision     string            `json:"revision"`
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
type Heartbeat struct {
	Observer   string    `json:"observer"`
	Boot       string    `json:"boot"`
	SampledAt  time.Time `json:"sampled_at"`
	ReceivedAt time.Time `json:"received_at"`
}
type Batch struct {
	Project      string        `json:"project"`
	Revision     string        `json:"revision"`
	Observations []Observation `json:"observations"`
	Heartbeat    *Heartbeat    `json:"heartbeat,omitempty"`
}
type CheckState struct {
	ID        string       `json:"id"`
	Observer  string       `json:"observer"`
	Health    string       `json:"health"`    // up,degraded,down,unknown
	Freshness string       `json:"freshness"` // fresh,stale,never,paused,maintenance
	Evidence  string       `json:"evidence"`
	Reason    string       `json:"reason"`
	Failures  int          `json:"failures"`
	Successes int          `json:"successes"`
	Since     time.Time    `json:"since"`
	Last      *Observation `json:"last,omitempty"`
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
type ObserverState struct {
	ID       string    `json:"id"`
	Health   string    `json:"health"`
	LastSeen time.Time `json:"last_seen"`
	Reason   string    `json:"reason"`
}
type Snapshot struct {
	Schema       int             `json:"schema"`
	Version      string          `json:"version"`
	Project      string          `json:"project"`
	Instance     string          `json:"instance"`
	Revision     string          `json:"revision"`
	GeneratedAt  time.Time       `json:"generated_at"`
	ViewObserver string          `json:"view_observer,omitempty"`
	Topology     Config          `json:"topology"`
	Checks       []CheckState    `json:"checks"`
	Nodes        []EntityState   `json:"nodes"`
	Services     []EntityState   `json:"services"`
	Paths        []EntityState   `json:"paths"`
	Links        []EntityState   `json:"links"`
	Observers    []ObserverState `json:"observers"`
	Incidents    []Incident      `json:"incidents"`
}
