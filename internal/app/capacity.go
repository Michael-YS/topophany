package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"math"
	"os"
	"time"
)

type capacityRow struct {
	model.StorageObserverUsage
	SamplesPerHour  float64  `json:"scheduled_samples_per_hour"`
	MinimumReceipts int64    `json:"scheduled_receipts"`
	MinimumBytes    int64    `json:"minimum_receipt_bytes"`
	SuggestedWeight int      `json:"suggested_weight"`
	Warnings        []string `json:"warnings"`
}
type capacityReport struct {
	Project       string        `json:"project"`
	SnapshotAt    time.Time     `json:"snapshot_at,omitzero"`
	StateBytes    int64         `json:"state_bytes"`
	MaxStateBytes int64         `json:"max_state_bytes"`
	Observers     []capacityRow `json:"observers"`
	Warnings      []string      `json:"warnings"`
	Note          string        `json:"note"`
}

// capacity plans against a reviewed topology and optionally an authenticated
// snapshot saved before migration. It never opens, locks or rewrites live data.
func (a *App) capacity(args []string) error {
	f := a.flags("capacity")
	path := f.String("config", "topology.json", "reviewed proposed topology")
	snapshot := f.String("snapshot", "", "optional saved topo --json snapshot for migration comparison")
	retention := f.Duration("retention", 24*time.Hour, "proposed ingestion window")
	state := f.Int64("state-bytes", 128<<20, "proposed state budget, at most 128 MiB")
	receipts := f.Int("max-receipts", 500000, "proposed receipt count limit")
	history := f.Int("max-history", 20000, "proposed raw history count limit")
	if err := parsed(f, args); err != nil {
		return err
	}
	if *retention < time.Minute || *retention > 30*24*time.Hour {
		return errors.New("retention must be 1m..720h")
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	budgets, err := store.Budgets(store.Options{Observers: observerIDs(c), ObserverWeights: config.StorageWeights(c), Retention: *retention, MaxStateBytes: *state, MaxReceipts: *receipts, MaxHistory: *history})
	if err != nil {
		return err
	}
	var previous model.Snapshot
	if *snapshot != "" {
		file, e := os.Open(*snapshot)
		if e != nil {
			return e
		}
		defer file.Close()
		if e = secure.Decode(file, &previous, model.MaxSnapshotBytes); e != nil {
			return e
		}
		if previous.Project != c.Project || previous.Storage == nil {
			return errors.New("snapshot needs matching project and collector storage usage")
		}
	}
	report := planCapacity(c, budgets, previous)
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	if err = enc.Encode(report); err != nil {
		return err
	}
	if len(report.Warnings) > 0 {
		return &ExitError{Code: ExitAttention}
	}
	for _, row := range report.Observers {
		if len(row.Warnings) > 0 {
			return &ExitError{Code: ExitAttention}
		}
	}
	return nil
}
func planCapacity(c model.Config, budgets store.Usage, previous model.Snapshot) capacityReport {
	rates := map[string]float64{}
	for _, x := range c.Checks {
		if !x.Disabled && !x.Maintenance {
			rates[x.Observer] += 3600 / float64(x.IntervalSeconds)
		}
	}
	minimum := math.Inf(1)
	for _, rate := range rates {
		if rate > 0 {
			minimum = math.Min(minimum, rate)
		}
	}
	held := map[string]model.StorageObserverUsage{}
	r := capacityReport{Project: c.Project, MaxStateBytes: budgets.MaxStateBytes, SnapshotAt: previous.GeneratedAt, Observers: []capacityRow{}, Warnings: []string{}, Note: "Receipt bytes are a lower bound; retained observations, state tails, floors, rollups and process memory require additional headroom. This command does not modify collector data or topology."}
	if previous.Storage != nil {
		r.StateBytes = previous.Storage.StateBytes
		for _, u := range previous.Storage.Observers {
			held[u.Observer] = u
		}
		if r.StateBytes > r.MaxStateBytes {
			r.Warnings = append(r.Warnings, "existing state exceeds proposed global budget")
		}
	}
	for _, quota := range budgets.Observers {
		row := capacityRow{StorageObserverUsage: quota, SamplesPerHour: rates[quota.Observer], SuggestedWeight: 1, Warnings: []string{}}
		old := held[quota.Observer]
		row.Receipts = old.Receipts
		row.Bytes = old.Bytes
		// Include one startup sample per active check in addition to steady state.
		for _, x := range c.Checks {
			if x.Observer == quota.Observer && !x.Disabled && !x.Maintenance {
				row.MinimumReceipts += int64(math.Ceil(budgets.Hours*3600/float64(x.IntervalSeconds))) + 1
			}
		}
		row.MinimumBytes = row.MinimumReceipts * 512
		if row.SamplesPerHour > 0 {
			row.SuggestedWeight = min(1000, int(math.Ceil(row.SamplesPerHour/minimum)))
		}
		if row.Bytes > row.ByteLimit || row.Receipts > row.ReceiptLimit {
			row.Warnings = append(row.Warnings, "existing records exceed proposed observer allocation; new samples may be refused after migration")
		}
		if row.MinimumBytes >= row.ByteLimit || row.MinimumReceipts >= int64(row.ReceiptLimit) {
			row.Warnings = append(row.Warnings, "scheduled receipts alone exhaust allocation; increase storage_weight, reduce sampling or shorten retention")
		}
		r.Observers = append(r.Observers, row)
	}
	if previous.Storage != nil {
		for id, u := range held {
			found := false
			for _, o := range budgets.Observers {
				found = found || o.Observer == id
			}
			if !found && (u.Bytes > 0 || u.Receipts > 0) {
				r.Warnings = append(r.Warnings, fmt.Sprintf("removed observer %s still holds records in the saved snapshot", id))
			}
		}
	}
	return r
}
