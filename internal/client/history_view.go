package client

import (
	"context"
	"errors"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// HistoryView binds a read to a snapshot's source and public measurement
// identity. It contains values only, so a background request cannot follow a
// concurrent UI switch to another snapshot or collector.
type HistoryView struct {
	Server   string
	Project  string
	Revision string
	Check    string
	Contract string
}

func SnapshotHistory(s model.Snapshot, server, check string) HistoryView {
	v := HistoryView{Server: server, Project: s.Project, Revision: s.Revision, Check: check}
	for _, state := range s.Checks {
		if state.ID == check {
			v.Contract = state.Contract
			break
		}
	}
	return v
}

func (v HistoryView) Match(h History, server string) error {
	if v.Server == "" || v.Project == "" || len(v.Revision) != 64 || len(v.Contract) != 64 || v.Server != server || v.Project != h.Project || v.Revision != h.Revision || v.Check != h.Check || v.Contract != h.Contract {
		return errors.New("history source or contract differs from the current snapshot; refresh the view")
	}
	return nil
}

// HistoryAt reads from the snapshot's collector only. Standalone History keeps
// ordered read failover; a snapshot-backed view must first accept a new snapshot
// before displaying another collector's samples.
func (c *Client) HistoryAt(ctx context.Context, v HistoryView, limit int) (History, string, error) {
	if v.Server == "" || v.Project != c.profile.Project || len(v.Revision) != 64 || len(v.Contract) != 64 {
		return History{}, "", errors.New("history needs a current snapshot source and contract")
	}
	return c.history(ctx, v.Check, limit, &v)
}
