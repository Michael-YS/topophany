package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// A check's contract identifies what it measures: the probe it runs, from
// which observer, against which target, through which proxies, and the
// assertions that decide its outcome. A sample counts only under the
// contract it was taken with. Everything else (names, notes, places,
// thresholds, schedules, what the check is attributed to, other checks)
// can change without touching the contract, so the check keeps its
// history and in-flight samples stay valid.
type contract struct {
	Schema   int                  `json:"schema"`
	Project  string               `json:"project"`
	Check    string               `json:"check"`
	Observer string               `json:"observer"`
	Kind     string               `json:"kind"`
	Target   string               `json:"target"`
	Timeout  int                  `json:"timeout_seconds"`
	BindIP   string               `json:"bind_ip,omitempty"`
	Resolve  string               `json:"resolve_ip,omitempty"`
	Server   string               `json:"server_name,omitempty"`
	CAFile   string               `json:"ca_file,omitempty"`
	HTTP     model.HTTPOptions    `json:"http"`
	DNS      model.DNSOptions     `json:"dns"`
	Adapter  model.AdapterOptions `json:"adapter"`
	Proxies  []model.Proxy        `json:"proxies,omitempty"`
}

// Contract is a check's contract in c. Executors stamp it on their samples
// and collectors store and count samples by it; like Revision, it is never
// shown to viewers, who see PublicContract.
func Contract(c model.Config, x model.Check) string {
	b, _ := json.Marshal(contract{Schema: 2, Project: c.Project, Check: x.ID, Observer: x.Observer, Kind: x.Kind, Target: Target(c, x), Timeout: x.TimeoutSeconds, BindIP: x.BindIP, Resolve: x.ResolveIP, Server: x.ServerName, CAFile: x.CAFile, HTTP: x.HTTP, DNS: x.DNS, Adapter: x.Adapter, Proxies: Path(c, x.Path).Proxies})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Contracts are the contracts of every check in c, by check ID.
func Contracts(c model.Config) map[string]string {
	out := make(map[string]string, len(c.Checks))
	for _, x := range c.Checks {
		out[x.ID] = Contract(c, x)
	}
	return out
}

// PublicContracts are the contracts of the redacted topology, which viewers
// may see: like PublicRevision, they permit no guess at hidden fields.
func PublicContracts(c model.Config) map[string]string { return Contracts(Public(c)) }

// ObserverContracts digests the contracts of an observer's checks, so a
// collector can tell whether an executor measures what it would count.
func ObserverContracts(c model.Config, observer string) string {
	pairs := []string{}
	for _, x := range c.Checks {
		if x.Observer == observer {
			pairs = append(pairs, x.ID+"="+Contract(c, x))
		}
	}
	sort.Strings(pairs)
	b, _ := json.Marshal(pairs)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Counted reports whether an observation counts in c under contracts, the
// contracts of c: it carries its check's contract, or it is a schema 1
// observation taken under exactly this topology.
func Counted(v model.Observation, contracts map[string]string, revision string) bool {
	if v.Contract != "" {
		return v.Contract == contracts[v.Check]
	}
	return v.Revision != "" && v.Revision == revision
}
