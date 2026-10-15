package client

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestSnapshotRequiresEveryStateInItsView(t *testing.T) {
	for _, observer := range []string{"", "home", "cloud"} {
		for name, omit := range map[string]func(*model.Snapshot){
			"check":    func(s *model.Snapshot) { s.Checks = s.Checks[1:] },
			"node":     func(s *model.Snapshot) { s.Nodes = s.Nodes[1:] },
			"endpoint": func(s *model.Snapshot) { s.Endpoints = s.Endpoints[1:] },
			"service":  func(s *model.Snapshot) { s.Services = s.Services[1:] },
			"path":     func(s *model.Snapshot) { s.Paths = s.Paths[1:] },
			"link":     func(s *model.Snapshot) { s.Links = s.Links[1:] },
			"observer": func(s *model.Snapshot) { s.Observers = s.Observers[1:] },
		} {
			t.Run(observer+"/"+name, func(t *testing.T) {
				s := engine.Build(testkit.Config(), "fixture", observer, nil, nil, time.Now())
				raw, _ := json.Marshal(s)
				var got model.Snapshot
				if e := decodeSnapshot(raw, &got); e != nil {
					t.Fatal("complete view refused", e)
				}
				omit(&s)
				raw, _ = json.Marshal(s)
				if decodeSnapshot(raw, &got) == nil {
					t.Fatal("incomplete view accepted")
				}
			})
		}
	}
}
