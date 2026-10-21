package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// legacyMeasurement is the contract representation before unused option blocks were removed.
type legacyMeasurement struct {
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
	Host     *model.HostOptions   `json:"host,omitempty"`
	Proxies  []model.Proxy        `json:"proxies,omitempty"`
}

func TestContractUpgradeRejectsLegacyAndKeepsStoredHistory(t *testing.T) {
	s, h, b := fixture(t)
	x, _ := config.Check(s.Config, b.Observations[0].Check)
	raw, err := json.Marshal(legacyMeasurement{Schema: 2, Project: s.Config.Project, Check: x.ID, Observer: x.Observer, Kind: x.Kind, Target: config.Target(s.Config, x), Timeout: x.TimeoutSeconds, BindIP: x.BindIP, Resolve: x.ResolveIP, Server: x.ServerName, CAFile: x.CAFile, HTTP: x.HTTP, DNS: x.DNS, Adapter: x.Adapter, Host: x.Host, Proxies: config.Path(s.Config, x.Path).Proxies})
	if err != nil {
		t.Fatal(err)
	}
	legacy := fmt.Sprintf("%x", sha256.Sum256(raw))
	current := b.Observations[0].Contract
	if legacy == current {
		t.Fatal("fixture must exercise a changed contract")
	}
	b.Observations[0].Contract = legacy
	if err := s.Store.Put(b); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 409 {
		t.Fatal("old worker accepted", w.Code, w.Body)
	}
	if rows := s.Store.History(x.ID, map[string]string{x.ID: current}, b.Revision, 100); len(rows) != 0 {
		t.Fatal("old measurement counted as current", rows)
	}
	if rows := s.Store.History(x.ID, map[string]string{x.ID: legacy}, b.Revision, 100); len(rows) != 1 {
		t.Fatal("legacy history lost", rows)
	}
	b.Observations[0].Contract = current
	b.Observations[0].Sequence++
	b.Observations[0].ID = fmt.Sprintf("%s.%s.%d", b.Observations[0].Observer, b.Observations[0].Boot, b.Observations[0].Sequence)
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
}
