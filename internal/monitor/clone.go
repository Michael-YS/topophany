package monitor

import "slices"

func copyPointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
func cloneObservers(in []ObserverVerdict) []ObserverVerdict {
	out := slices.Clone(in)
	for i := range out {
		out[i].LastSeen = copyPointer(out[i].LastSeen)
		out[i].ValidUntil = copyPointer(out[i].ValidUntil)
	}
	return out
}

// Values are published as owned snapshots, including nested evidence payloads.
// A callback or subscriber may edit its copy without changing the hub or source.
func clone(p Panel) Panel {
	p.Error = copyPointer(p.Error)
	p.Rows = slices.Clone(p.Rows)
	for i := range p.Rows {
		r := &p.Rows[i]
		r.Limit = copyPointer(r.Limit)
		switch v := r.Value.(type) {
		case []ObserverVerdict:
			r.Value = cloneObservers(v)
		case AttentionList:
			v.Items = slices.Clone(v.Items)
			for i := range v.Items {
				v.Items[i].Route = slices.Clone(v.Items[i].Route)
				v.Items[i].Since = copyPointer(v.Items[i].Since)
			}
			r.Value = v
		case []ApplicationValue:
			v = slices.Clone(v)
			for i := range v {
				v[i].Services = slices.Clone(v[i].Services)
			}
			r.Value = v
		case ServerCard:
			v.Resources = slices.Clone(v.Resources)
			v.Services = slices.Clone(v.Services)
			v.Observes = cloneObservers(v.Observes)
			for i := range v.Resources {
				x := &v.Resources[i]
				x.SampledAt = copyPointer(x.SampledAt)
				x.ValidUntil = copyPointer(x.ValidUntil)
				x.CPU.Percent = copyPointer(x.CPU.Percent)
				x.CPU.Limit = copyPointer(x.CPU.Limit)
				x.CPU.Logical = copyPointer(x.CPU.Logical)
				x.Memory.Percent = copyPointer(x.Memory.Percent)
				x.Memory.Limit = copyPointer(x.Memory.Limit)
				x.Memory.Used = copyPointer(x.Memory.Used)
				x.Memory.Total = copyPointer(x.Memory.Total)
				x.Disk.Percent = copyPointer(x.Disk.Percent)
				x.Disk.Limit = copyPointer(x.Disk.Limit)
				x.Disk.Used = copyPointer(x.Disk.Used)
				x.Disk.Total = copyPointer(x.Disk.Total)
				x.Process = copyPointer(x.Process)
				if x.Process != nil {
					x.Process.Matching = copyPointer(x.Process.Matching)
				}
			}
			r.Value = v
		}
	}
	return p
}

// Clone returns an independently writable panel, including nested metric values.
func (p Panel) Clone() Panel { return clone(p) }
