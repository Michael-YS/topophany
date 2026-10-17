package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func TestShowRejectsHistoryOutsideSnapshotContext(t *testing.T) {
	now := time.Now()
	snap := engine.Build(testkit.Config(), "fixture", "", nil, nil, now)
	check := snap.Checks[0]
	for _, kind := range []string{"matching", "source", "revision", "contract"} {
		t.Run(kind, func(t *testing.T) {
			h := client.History{Project: snap.Project, Revision: snap.Revision, Check: check.ID, Contract: check.Contract, Entries: []model.Observation{}}
			server := "collector-a"
			switch kind {
			case "source":
				server = "collector-b"
			case "revision":
				h.Revision = strings.Repeat("a", 64)
			case "contract":
				h.Contract = strings.Repeat("b", 64)
			}
			r := reader{history: func(context.Context, string, int) (client.History, string, error) { return h, server, nil }}
			var out bytes.Buffer
			a := &App{Out: &out}
			if err := a.showEntry(context.Background(), r, view.View{Snapshot: snap, Now: now}, header{Server: "collector-a"}, view.Checks, check.ID, true); err != nil {
				t.Fatal(err)
			}
			var got tabJSON
			if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.Rows) != 1 {
				t.Fatal("invalid show output", err, out.String())
			}
			row := got.Rows[0]
			if kind == "matching" {
				if row.Samples == nil || row.SamplesError != "" {
					t.Fatal("matching snapshot history refused", row.SamplesError)
				}
			} else if row.Samples != nil || row.SamplesError == "" {
				t.Fatal("mismatched history rendered under current check", out.String())
			}
		})
	}
}
