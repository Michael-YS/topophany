package engine

import "github.com/WhitenWhiten/topophany/internal/model"

const counterEvidenceLimit = 32

type responseProof struct{ check, observer string }
type responseProofIndex map[string][]responseProof

// Index fresh answers once per snapshot. At most 32 per observer and 64 in
// total suffice to return 32 proofs after excluding any one observer. The
// scan is linear in configured checks; each lookup inspects at most 64 IDs.
func (p *Plan) responseProofs(states map[string]model.CheckState) responseProofIndex {
	out := responseProofIndex{}
	counts := map[string]map[string]int{}
	for _, x := range p.config.Checks {
		ep, ok := p.endpoints[x.Endpoint]
		if !ok {
			continue
		}
		node := ep.Node
		if len(ep.Via) > 0 {
			node = ep.Via[0]
		}
		if len(out[node]) == 2*counterEvidenceLimit || counts[node][x.Observer] == counterEvidenceLimit {
			continue
		}
		s := states[x.ID]
		if s.Freshness != "fresh" || s.Last == nil || !responded(s.Last) {
			continue
		}
		if counts[node] == nil {
			counts[node] = map[string]int{}
		}
		out[node] = append(out[node], responseProof{x.ID, x.Observer})
		counts[node][x.Observer]++
	}
	return out
}

func (p responseProofIndex) excluding(node, observer string) []string {
	var out []string
	for _, proof := range p[node] {
		if proof.observer != observer {
			out = append(out, proof.check)
			if len(out) == counterEvidenceLimit {
				break
			}
		}
	}
	return out
}
