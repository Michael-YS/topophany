package view

import (
	_ "embed"
	"encoding/json"
	"sync"
)

//go:embed direct.en.json
var directWordsJSON []byte
var directWords map[string]string
var directWordsOnce sync.Once

func DirectText(key string) string {
	directWordsOnce.Do(func() { _ = json.Unmarshal(directWordsJSON, &directWords) })
	return directWords[key]
}
func (v View) directVerdict() Verdict {
	status, _ := v.SourcesStatus()
	glyph := "○"
	if status.State == "ready" {
		glyph = "●"
	} else if status.State == "partial" {
		glyph = "▲"
	}
	return Verdict{State: status.State, Glyph: glyph, Text: DirectText(status.State)}
}
