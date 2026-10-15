package store

import (
	"container/heap"
	"strings"
	"time"
)

// A floorQueue holds the floors of one observer, the one whose receipt was
// taken longest ago first, so what an observer holds is counted, and the
// floors it must shed are found, without visiting every floor the store
// holds. A worker picks its boot IDs, so it can have the store hold as
// many floors as its share allows, and every write prunes under the lock
// all observers' ingestion waits on.
type floorQueue struct {
	floors []heldFloor
	index  map[string]int // by boot, its place in floors
}
type heldFloor struct {
	boot string
	at   time.Time
}

func (q *floorQueue) Len() int { return len(q.floors) }
func (q *floorQueue) Less(i, j int) bool {
	if c := q.floors[i].at.Compare(q.floors[j].at); c != 0 {
		return c < 0
	}
	return strings.Compare(q.floors[i].boot, q.floors[j].boot) < 0
}
func (q *floorQueue) Swap(i, j int) {
	q.floors[i], q.floors[j] = q.floors[j], q.floors[i]
	q.index[q.floors[i].boot], q.index[q.floors[j].boot] = i, j
}
func (q *floorQueue) Push(x any) {
	f := x.(heldFloor)
	q.index[f.boot] = len(q.floors)
	q.floors = append(q.floors, f)
}
func (q *floorQueue) Pop() any {
	f := q.floors[len(q.floors)-1]
	q.floors = q.floors[:len(q.floors)-1]
	delete(q.index, f.boot)
	return f
}

// raiseFloor binds boot's IDs up to seq, from a receipt taken at at.
func (s *Store) raiseFloor(boot string, seq uint64, at time.Time) {
	s.floors[boot] = max(s.floors[boot], seq)
	if at.After(s.floorAt[boot]) {
		s.floorAt[boot] = at
	}
	observer := receiptObserver(boot + ".0")
	q := s.floorQueues[observer]
	if q == nil {
		q = &floorQueue{index: map[string]int{}}
		s.floorQueues[observer] = q
	}
	if i, ok := q.index[boot]; ok {
		q.floors[i].at = s.floorAt[boot]
		heap.Fix(q, i)
		return
	}
	heap.Push(q, heldFloor{boot, s.floorAt[boot]})
}

// queueFloors derives each observer's floor queue from the floors held.
func (s *Store) queueFloors() {
	s.floorQueues = map[string]*floorQueue{}
	for boot := range s.floors {
		observer := receiptObserver(boot + ".0")
		q := s.floorQueues[observer]
		if q == nil {
			q = &floorQueue{index: map[string]int{}}
			s.floorQueues[observer] = q
		}
		q.index[boot] = len(q.floors)
		q.floors = append(q.floors, heldFloor{boot, s.floorAt[boot]})
	}
	for _, q := range s.floorQueues {
		heap.Init(q)
	}
}

// floorLimit is how many floors an observer may hold: one floorShare-th
// of its share of the state budget.
func (s *Store) floorLimit(observer ...string) int {
	if len(observer) > 0 {
		return int(max(s.options.share(s.options.MaxStateBytes, observer[0])/floorShare/floorBytes, 1))
	}
	n := int64(max(len(s.options.Observers), 1))
	return int(max(s.options.MaxStateBytes/n/floorShare/floorBytes, 1))
}

// shedFloors lets go of the n floors of observer whose receipts were taken
// longest ago. A worker picks its boot IDs, so one that changed its boot
// without end would otherwise fill its observer's share with floors for
// good, past rotating its token or restarting the collector; a boot still
// delivering keeps raising its floor, and keeps it. The IDs a shed floor
// bound were received more than the ingestion window and an hour ago, so
// their samples are too old to be accepted again: only another sample
// under one of those IDs, which only that observer's worker would send,
// is no longer refused.
func (s *Store) shedFloors(observer string, n int) {
	q := s.floorQueues[observer]
	for ; n > 0 && q.Len() > 0; n-- {
		f := heap.Pop(q).(heldFloor)
		delete(s.floors, f.boot)
		delete(s.floorAt, f.boot)
	}
	if q.Len() == 0 {
		delete(s.floorQueues, observer)
	}
}
