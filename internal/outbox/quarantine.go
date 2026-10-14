package outbox

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Quarantine has its own global budget and per-recipient shares. Oldest evidence
// is retired first; pending deliveries are never removed by this retention rule.
// At most one extra record can be present during an interrupted atomic write.
func (q *Queue) trimQuarantine(recipient, keep string, extra int64) error {
	type entry struct {
		name, recipient string
		size            int64
		at              time.Time
	}
	dir := filepath.Join(q.dir, "quarantine")
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	entries := []entry{}
	sizes := map[string]int64{}
	counts := map[string]int{}
	count := 0
	total := int64(0)
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		name := f.Name()
		if len(name) != 86 || name[64] != '-' || strings.Trim(name[:64]+name[65:81], "0123456789abcdef") != "" || f.Type()&os.ModeSymlink != 0 || f.IsDir() {
			return errors.New("invalid quarantine entry")
		}
		info, err := f.Info()
		if err != nil {
			return err
		}
		id := name[65:81]
		entries = append(entries, entry{name, id, info.Size(), info.ModTime()})
		sizes[id] += info.Size()
		counts[id]++
		count++
		total += info.Size()
	}
	if keep != "" {
		found := false
		for _, v := range entries {
			if v.name == keep {
				found = true
				break
			}
		}
		if !found {
			counts[recipient]++
			count++
		}
	}
	sizes[recipient] += extra
	total += extra
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].at.Equal(entries[j].at) {
			return entries[i].name < entries[j].name
		}
		return entries[i].at.Before(entries[j].at)
	})
	perRecipient := max(q.max/8, 2<<20)
	global := max(q.max, 16<<20)
	removed := false
	for _, v := range entries {
		if v.name == keep {
			continue
		}
		if sizes[v.recipient] <= perRecipient && total <= global && counts[v.recipient] <= 1024 && count <= 8192 {
			continue
		}
		if err := os.Remove(filepath.Join(dir, v.name)); err != nil {
			return err
		}
		sizes[v.recipient] -= v.size
		counts[v.recipient]--
		count--
		total -= v.size
		q.used -= v.size
		q.quarantineUsed -= v.size
		removed = true
	}
	if removed {
		if err := secure.SyncDir(dir); err != nil {
			return err
		}
	}
	if total > global || sizes[recipient] > perRecipient {
		return errors.New("quarantine record exceeds independent budget")
	}
	return nil
}
