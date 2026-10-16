package outbox

import (
	"os"
	"time"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func queueReadDir(path string) ([]os.DirEntry, error) {
	start := time.Now()
	v, e := os.ReadDir(path)
	ioaccount.Queue.Add(0, time.Since(start), e)
	return v, e
}
func queueRead(path string, limit int64) ([]byte, error) {
	start := time.Now()
	v, e := secure.ReadBounded(path, limit)
	ioaccount.Queue.Add(0, time.Since(start), e)
	return v, e
}
func queueStat(path string) (os.FileInfo, error) {
	start := time.Now()
	v, e := os.Stat(path)
	ioaccount.Queue.Add(0, time.Since(start), e)
	return v, e
}
func queueRemove(path string) error {
	start := time.Now()
	e := os.Remove(path)
	ioaccount.Queue.Add(0, time.Since(start), e)
	return e
}
func queueWrite(path string, b []byte, mode os.FileMode) error {
	return secure.AtomicWriteMeasured(path, b, mode, &ioaccount.Queue)
}
func queueInfo(entry os.DirEntry) (os.FileInfo, error) {
	started := time.Now()
	v, e := entry.Info()
	ioaccount.Queue.Add(0, time.Since(started), e)
	return v, e
}
