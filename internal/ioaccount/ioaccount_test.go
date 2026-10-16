package ioaccount

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestConcurrentCounters(t *testing.T) {
	var c Counter
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { c.Add(5, time.Millisecond, nil) })
	}
	wg.Wait()
	c.Add(0, 2*time.Millisecond, errors.New("fixture"))
	v := c.Read()
	if v.Count != 101 || v.Bytes != 500 || v.Errors != 1 || v.Nanoseconds != uint64(102*time.Millisecond) {
		t.Fatal(v)
	}
}
