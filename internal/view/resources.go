package view

import (
	"fmt"
	"maps"
	"math"
	"strconv"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// CardResource is local telemetry for a check on this node's own observer.
// Facts are present only while the resource sample is current and readable.
type CardResource struct {
	Check     string            `json:"check"`
	Observer  Ref               `json:"observer"`
	Health    string            `json:"health"`
	Freshness string            `json:"freshness"`
	SampledAt time.Time         `json:"sampled_at,omitzero"`
	Facts     map[string]string `json:"facts,omitempty"`
}

func (v View) HostResources() map[string][]CardResource {
	out := map[string][]CardResource{}
	observers := map[string]model.Observer{}
	checks := map[string]model.Check{}
	for _, o := range v.Snapshot.Topology.Observers {
		observers[o.ID] = o
	}
	for _, x := range v.Snapshot.Topology.Checks {
		if x.Kind == "host" {
			checks[x.ID] = x
		}
	}
	for _, cs := range v.Snapshot.Checks {
		x, ok := checks[cs.ID]
		o := observers[x.Observer]
		if !ok || o.Node == "" {
			continue
		}
		out[o.Node] = append(out[o.Node], v.hostResource(cs, x, o))
	}
	return out
}

// HostResource is a host check's local telemetry, whether or not its
// observer is on a node.
func (v View) HostResource(id string) (CardResource, bool) {
	for _, x := range v.Snapshot.Topology.Checks {
		if x.ID != id || x.Kind != "host" {
			continue
		}
		for _, cs := range v.Snapshot.Checks {
			if cs.ID == id {
				for _, o := range v.Snapshot.Topology.Observers {
					if o.ID == x.Observer {
						return v.hostResource(cs, x, o), true
					}
				}
				return v.hostResource(cs, x, model.Observer{ID: x.Observer}), true
			}
		}
	}
	return CardResource{}, false
}

func (v View) hostResource(cs model.CheckState, x model.Check, o model.Observer) CardResource {
	name := o.Name
	if name == "" {
		name = o.ID
	}
	r := CardResource{Check: cs.ID, Observer: Ref{o.ID, name}, Health: cs.Health, Freshness: cs.Freshness}
	if cs.Last != nil {
		r.SampledAt = cs.Last.SampledAt
	}
	current := !v.Cached && !v.Draft && !Expired(v.Snapshot, v.Now) && cs.Freshness == "fresh" && cs.Last != nil && !cs.Last.SampledAt.After(v.Now.Add(model.SampleClockAhead)) && v.Now.Sub(cs.Last.SampledAt) <= time.Duration(x.StaleSeconds)*time.Second
	if current && (cs.Last.Outcome == "ok" || cs.Last.Outcome == "policy") && cs.Last.Evidence == "reported" {
		r.Facts = maps.Clone(cs.Last.Facts)
	} else {
		r.Health = "unknown"
	}
	if v.Cached || Expired(v.Snapshot, v.Now) {
		r.Freshness = "cached"
	}
	return r
}

// ResourceValues are the same compact summaries on a card and in CLI output.
func ResourceValues(r CardResource) [][2]string {
	f := r.Facts
	percent := func(key string) string {
		n, err := strconv.ParseFloat(f[key], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
			return "unknown"
		}
		return fmt.Sprintf("%.1f%%", n)
	}
	capacity := func(prefix string) string {
		p := percent(prefix + "_used_percent")
		used, e1 := strconv.ParseUint(f[prefix+"_used_bytes"], 10, 64)
		total, e2 := strconv.ParseUint(f[prefix+"_total_bytes"], 10, 64)
		if p == "unknown" || e1 != nil || e2 != nil || total == 0 || used > total {
			return "unknown"
		}
		return p + " · " + resourceBytes(used) + "/" + resourceBytes(total)
	}
	processes := "unknown"
	if n, err := strconv.ParseUint(f["process_count"], 10, 64); err == nil {
		processes = strconv.FormatUint(n, 10) + " total"
		if n, err := strconv.ParseUint(f["process_matching_count"], 10, 64); err == nil && f["process_name"] != "" {
			processes += " · " + f["process_name"] + " " + strconv.FormatUint(n, 10)
		}
	}
	return [][2]string{{"CPU", percent("cpu_used_percent")}, {"Memory", capacity("memory")}, {"Disk", capacity("disk")}, {"Processes", processes}}
}

func resourceBytes(n uint64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1fGiB", float64(n)/(1<<30))
	}
	if n >= 1<<20 {
		return fmt.Sprintf("%.1fMiB", float64(n)/(1<<20))
	}
	return strconv.FormatUint(n, 10) + "B"
}
