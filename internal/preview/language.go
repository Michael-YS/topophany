package preview

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// The built-in topology and the scenarios are written once, without words:
// every text in topology.json that a reader sees in their own language is a
// reference such as "@node.win", and locales/<language>.json gives each
// reference its text, as do scenario.<id> and scenario.<id>.detail for the
// scenarios. A language is supported when its catalog is there.

//go:embed locales/*.json
var locales embed.FS

// DefaultLanguage is the language used when none of the supported ones is
// asked for.
const DefaultLanguage = "en"

// reference is a whole JSON string that names a catalog entry.
var reference = regexp.MustCompile(`"@([a-z0-9][a-z0-9.-]*)"`)

// Languages lists the supported languages, sorted.
func Languages() []string {
	files, _ := locales.ReadDir("locales")
	r := []string{}
	for _, f := range files {
		if lang, ok := strings.CutSuffix(f.Name(), ".json"); ok {
			r = append(r, lang)
		}
	}
	slices.Sort(r)
	return r
}

// Language is the supported language for tag, a BCP 47 tag such as "zh-CN"
// or a POSIX locale such as "ja_JP.UTF-8": the one named by the whole tag,
// else by its primary subtag. It is "" when none is supported.
func Language(tag string) string {
	tag = strings.ToLower(strings.ReplaceAll(tag, "_", "-"))
	if i := strings.IndexAny(tag, ".@"); i >= 0 {
		tag = tag[:i]
	}
	primary, _, _ := strings.Cut(tag, "-")
	langs := Languages()
	for _, want := range []string{tag, primary} {
		if want != "" && slices.Contains(langs, want) {
			return want
		}
	}
	return ""
}

// catalog is the text of every reference in lang.
func catalog(lang string) (map[string]string, error) {
	if Language(lang) != lang {
		return nil, fmt.Errorf("preview: no %q text; languages are %s", lang, strings.Join(Languages(), ", "))
	}
	b, e := locales.ReadFile(path.Join("locales", lang+".json"))
	if e != nil {
		return nil, e
	}
	text := map[string]string{}
	if e := json.Unmarshal(b, &text); e != nil {
		return nil, fmt.Errorf("preview: locales/%s.json: %w", lang, e)
	}
	return text, nil
}

// localize replaces each reference in the JSON source with its text in lang.
func localize(source []byte, lang string) ([]byte, error) {
	text, e := catalog(lang)
	if e != nil {
		return nil, e
	}
	var missing []string
	out := reference.ReplaceAllFunc(source, func(ref []byte) []byte {
		key := string(ref[2 : len(ref)-1])
		s, ok := text[key]
		if !ok {
			missing = append(missing, key)
			return ref
		}
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.Encode(s)
		return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("preview: locales/%s.json has no text for %s", lang, strings.Join(missing, ", "))
	}
	return out, nil
}
