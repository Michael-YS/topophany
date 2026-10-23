package monitor

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/admission"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
)

const localExternalStateBytes = 32 << 20

// The worker's display state follows the same normalized report transaction
// as direct and collector readers, independently of outbox retention.
// The Local mutex is held throughout admission and publication.
func (l *Local) recordExternal(v model.Observation, x model.Check) {
	if x.External == nil {
		return
	}
	src, ok := l.sources[x.External.Source]
	if !ok {
		return
	}
	now := time.Now().UTC()
	normalized, err := admission.External(v, x, src, admission.Window{Now: now, Future: now, MaxAge: 24 * time.Hour, Skew: 30 * time.Second})
	if err != nil || v.External == nil {
		return
	}
	v = model.CloneObservation(v)
	v.ReceivedAt = now
	v.External.Report = normalized
	update := report.BeginState(l.external)
	update.Apply(v, *x.External, model.ExternalRule{FailureThreshold: x.FailureThreshold, RecoveryThreshold: x.RecoveryThreshold, PolicyThreshold: x.PolicyThreshold})
	sizes, err := l.externalSizes.Updated(update)
	if err != nil || sizes.Total > localExternalStateBytes {
		return
	}
	l.external, l.externalSizes = update.State, sizes
	l.externalPrepared = l.externalPrepared.WithExternalChanges(update.Sources, update.Bindings)
}
