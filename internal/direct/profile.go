package direct

import "slices"

// Profiles contain scalar source configuration plus owned selection/node slices.
// Copy those containers directly, without re-decoding or applying JSON defaults.
func cloneProfile(p Profile) Profile {
	out := p
	out.Sources = slices.Clone(p.Sources)
	for i := range out.Sources {
		out.Sources[i].Selected = slices.Clone(p.Sources[i].Selected)
		out.Sources[i].Nodes = slices.Clone(p.Sources[i].Nodes)
	}
	return out
}

// Compare content as well as the revision: embedded callers may supply edited
// profiles without minting a new disk revision. Display-only stream requests
// pass the same profile and must not rebuild accepted evidence or poll loops.
func sameProfile(a, b Profile) bool {
	return a.Profile == b.Profile && a.Project == b.Project && a.Revision == b.Revision && a.Dataset == b.Dataset && slices.EqualFunc(a.Sources, b.Sources, func(a, b Entry) bool {
		return a.Source == b.Source && a.AuthMode == b.AuthMode && a.Credential == b.Credential && a.Subject == b.Subject && slices.Equal(a.Selected, b.Selected) && slices.Equal(a.Nodes, b.Nodes)
	})
}

func sameAcquisition(a, b Entry) bool {
	a.Source.Name, b.Source.Name = "", ""
	if a.Source != b.Source || a.Subject != b.Subject {
		return false
	}
	if slices.Equal(a.Selected, b.Selected) {
		return true
	}
	// Selection order affects presentation, not the acquisition identity.
	left, right := slices.Clone(a.Selected), slices.Clone(b.Selected)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
