package monitor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// PrepareOutput canonicalizes parent aliases. Existing non-panel files and
// symlink targets are never overwritten, including on initial configuration.
func PrepareOutput(path, name string, protected ...string) (string, error) {
	path, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	for _, p := range protected {
		if p == "" {
			continue
		}
		p, e = filepath.Abs(p)
		if e != nil {
			return "", e
		}
		rel, err := filepath.Rel(p, path)
		if strings.EqualFold(p, path) || err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errors.New("monitor output overlaps protected configuration or data")
		}
	}
	switch strings.ToLower(filepath.Base(path)) {
	case "config.json", "template.json", "credentials.json", "worker-credentials.json", "auth.json", "topology.json", "checkpoint.json", "incidents.json":
		return "", errors.New("reserved monitor output name")
	}
	if filepath.Ext(path) != ".json" {
		return "", errors.New("monitor output must end in .json")
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return "", e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil {
		return "", e
	}
	path = filepath.Join(parent, filepath.Base(path))
	for _, p := range protected {
		if p == "" {
			continue
		}
		abs, _ := filepath.Abs(p)
		real, err := filepath.EvalSymlinks(abs)
		if err == nil {
			abs = real
		}
		rel, err := filepath.Rel(abs, path)
		if strings.EqualFold(path, abs) || err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errors.New("monitor output aliases protected configuration or data")
		}
	}
	if st, statErr := os.Lstat(path); statErr == nil {
		if !st.Mode().IsRegular() {
			return "", errors.New("monitor output must be a regular non-symlink file")
		}
		var existing Panel
		b, err := secure.ReadBounded(path, 1<<20)
		if err != nil {
			return "", err
		}
		if err = json.Unmarshal(b, &existing); err != nil || existing.Schema != 1 || existing.Name != name || existing.ConfigRevision == "" {
			return "", errors.New("refusing to overwrite a non-monitor file or another panel")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	return path, nil
}

type Output struct {
	path   string
	unlock func() error
	last   Panel
	digest string
}

func OpenOutput(path, name string, protected ...string) (*Output, error) {
	path, e := PrepareOutput(path, name, protected...)
	if e != nil {
		return nil, e
	}
	// Fold case even on case-sensitive filesystems; conservative contention is
	// safer than two locks for one target on case-insensitive volumes.
	lock := filepath.Join(filepath.Dir(path), ".topophany-monitor-"+secure.Hash(strings.ToLower(filepath.Base(path)))[:24]+".lock")
	unlock, e := secure.Lock(lock)
	if e != nil {
		return nil, e
	}
	if _, e = PrepareOutput(path, name, protected...); e != nil {
		unlock()
		return nil, e
	}
	return &Output{path: path, unlock: unlock}, nil
}
func (o *Output) Close() error { return o.unlock() }
func content(p Panel) string {
	p.GeneratedAt = time.Time{}
	p.PublisherExpiresAt = time.Time{}
	p.Sequence = 0
	// A refreshed source envelope alone does not constitute new observation
	// evidence; the publisher still renews the file within ten seconds.
	for i := range p.Rows {
		p.Rows[i].ValidUntil = time.Time{}
	}
	b, _ := json.Marshal(p)
	return secure.Hash(string(b))
}
func (o *Output) Write(p Panel, every time.Duration, force bool) error {
	p = clone(p)
	if !force && !ShouldPublish(o.last, o.digest, p, every) {
		return nil
	}
	digest := content(clone(p))
	p.PublisherExpiresAt = p.GeneratedAt.Add(30 * time.Second)
	if p.Lifecycle != "running" {
		p.PublisherExpiresAt = p.GeneratedAt
	}
	b, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	if e = secure.CacheWrite(o.path, append(b, '\n'), 0600); e != nil {
		return e
	}
	o.last = clone(p)
	o.digest = digest
	return nil
}

// ShouldPublish is the shared cadence policy for file and callback consumers.
// Health, freshness, reason and lifecycle transitions bypass display cadence;
// all content gets a lease renewal within ten seconds.
func ShouldPublish(last Panel, digest string, p Panel, every time.Duration) bool {
	transition := safetyState(last) != safetyState(p)
	elapsed := p.GeneratedAt.Sub(last.GeneratedAt)
	return transition || elapsed >= 10*time.Second || elapsed >= every && content(clone(p)) != digest
}

// ContentDigest excludes publication clocks and source-envelope deadlines.
func ContentDigest(p Panel) string { return content(clone(p)) }
