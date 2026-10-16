// Package preferences stores local presentation defaults, independently of
// topology, credentials and monitoring state.
package preferences

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Settings struct {
	Page            string `json:"page"`
	GroupsCollapsed bool   `json:"groups_collapsed"`
	UniformCards    bool   `json:"uniform_cards"`
	FullMap         bool   `json:"full_map"`
	MapMode         string `json:"map_mode"`
}

var Pages = []string{"overview", "topology", "servers", "services", "paths", "checks", "incidents", "observers", "about"}
var MapModes = []string{"auto", "virtual", "geo", "network"}

func Defaults() Settings { return Settings{Page: "overview", MapMode: "auto"} }
func (s Settings) Validate() error {
	if !slices.Contains(Pages, s.Page) {
		return fmt.Errorf("invalid page %q; choose %s", s.Page, strings.Join(Pages, ", "))
	}
	if !slices.Contains(MapModes, s.MapMode) {
		return fmt.Errorf("invalid map mode %q; choose %s", s.MapMode, strings.Join(MapModes, ", "))
	}
	return nil
}
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "topophany", "preferences.json"), nil
}
func Load(path string) (Settings, error) {
	s := Defaults()
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	defer f.Close()
	if err = secure.Decode(f, &s, 16384); err != nil {
		return s, fmt.Errorf("preferences: %w", err)
	}
	return s, s.Validate()
}
func Save(path string, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return secure.AtomicWrite(path, append(b, '\n'), 0600)
}
