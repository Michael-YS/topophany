package outbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// The ACK journal is separate from immutable payloads. One fsync commits a
// delivery batch; bounded compaction folds its partial ACKs into the records.
// Payload and recipient reservations remain conservative until compaction.
const maxAckJournal = 1 << 20

type ackRecord struct {
	Project   string   `json:"project"`
	Recipient string   `json:"recipient"`
	IDs       []string `json:"ids"`
	SHA256    string   `json:"sha256"`
}

func ackHash(r ackRecord) string {
	r.SHA256 = ""
	b, _ := json.Marshal(r)
	return secure.Hash(string(b))
}

func (q *Queue) openAcks() error {
	path := filepath.Join(q.dir, "acks.jsonl")
	raw, err := queueRead(path, maxAckJournal+16384)
	if errors.Is(err, os.ErrNotExist) {
		if err = queueWrite(path, nil, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	q.acked = map[string]bool{}
	offset := 0
	for len(raw) > 0 {
		end := bytes.IndexByte(raw, '\n')
		if end < 0 {
			break
		} // a torn final append was never acknowledged locally
		if end > 16384 {
			return errors.New("oversized outbox ACK record")
		}
		var r ackRecord
		if json.Unmarshal(raw[:end], &r) != nil || r.Project != q.project || r.SHA256 != ackHash(r) || len(r.IDs) > 100 || len(r.IDs) == 0 || len(r.Recipient) != 16 || strings.Trim(r.Recipient, "0123456789abcdef") != "" {
			return errors.New("outbox ACK journal corruption")
		}
		for _, id := range r.IDs {
			if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
				return errors.New("invalid outbox ACK identity")
			}
			q.acked[id] = true
			q.applyAck(id, r.Recipient)
		}
		offset += end + 1
		raw = raw[end+1:]
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	q.ackFile = f
	// Discard only an incomplete final record before any subsequent append.
	if err = f.Truncate(int64(offset)); err == nil {
		_, err = f.Seek(0, io.SeekEnd)
	}
	q.ackBytes = int64(offset)
	return err
}

func (q *Queue) applyAck(id, recipient string) {
	v, ok := q.items[id]
	if !ok || !slices.Contains(v.Pending, recipient) {
		return
	}
	v.Pending = slices.DeleteFunc(slices.Clone(v.Pending), func(r string) bool { return r == recipient })
	q.account(Item{ID: id, Pending: v.Pending, Batch: model.Batch{Observations: []model.Observation{{SampledAt: v.At}}}}, q.itemSizes[id])
}

func (q *Queue) logAcks(ids []string, recipient string) error {
	if len(ids) == 0 {
		return nil
	}
	if q.ackError != nil {
		return q.ackError
	}
	r := ackRecord{Project: q.project, Recipient: recipient, IDs: ids}
	r.SHA256 = ackHash(r)
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if q.ackBytes+int64(len(raw)) > maxAckJournal {
		if err = q.compactAcks(); err != nil {
			return err
		}
	}
	start := time.Now()
	n, err := q.ackFile.Write(raw)
	ioaccount.Queue.Add(n, time.Since(start), err)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = ioaccount.Sync(q.ackFile)
	}
	if err != nil {
		q.ackError = errors.New("outbox ACK durability failure; restart required")
		return q.ackError
	}
	q.ackBytes += int64(n)
	for _, id := range ids {
		q.acked[id] = true
		q.applyAck(id, recipient)
	}
	return nil
}

func (q *Queue) compactAcks() (result error) {
	defer func() {
		if result != nil {
			q.ackError = result
		}
	}()
	// Publish every changed pending list before truncating. A crash in either
	// phase replays ACKs idempotently and can only cause duplicate delivery.
	for id := range q.acked {
		if _, ok := q.items[id]; ok {
			v, err := q.loadItem(id)
			if err != nil {
				return err
			}
			if err := q.write(v); err != nil {
				return err
			}
		}
	}
	if err := q.ackFile.Truncate(0); err != nil {
		return err
	}
	if _, err := q.ackFile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := ioaccount.Sync(q.ackFile); err != nil {
		q.ackError = err
		return err
	}
	q.ackBytes = 0
	clear(q.acked)
	return nil
}
