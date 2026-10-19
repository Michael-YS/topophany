package client

import (
	"errors"
	"slices"

	"github.com/WhitenWhiten/topophany/internal/config"
)

// ProfileSummary is connection metadata safe to expose to profile pickers.
type ProfileSummary struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	Active  bool   `json:"active"`
}

// ListProfiles reads the same private, bounded file as Load, without resolving
// tokens, validating connections or contacting collectors. Discovery accepts
// empty profiles and projects; Load still requires a usable saved connection.
// The active profile comes first, then names in lexical order, at most 64.
func ListProfiles(path string) ([]ProfileSummary, error) {
	c, err := readCredentials(path)
	if err != nil {
		return nil, err
	}
	if c.Schema != 1 {
		return nil, errors.New("invalid credential schema")
	}
	out := []ProfileSummary{}
	if len(c.Profiles) == 0 {
		return out, nil
	}
	if !config.ID(c.Active) {
		return nil, errors.New("invalid active profile")
	}
	if _, ok := c.Profiles[c.Active]; !ok {
		return nil, errors.New("active profile missing")
	}
	names := make([]string, 0, len(c.Profiles))
	for name, p := range c.Profiles {
		if !config.ID(name) || p.Project != "" && !config.ID(p.Project) {
			return nil, errors.New("invalid profile name or project")
		}
		if name != c.Active {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	names = append([]string{c.Active}, names...)
	for _, name := range names[:min(64, len(names))] {
		out = append(out, ProfileSummary{Name: name, Project: c.Profiles[name].Project, Active: name == c.Active})
	}
	return out, nil
}
