package model

import "time"

type StorageUsage struct {
	Receipts      int                    `json:"receipts"`
	MaxReceipts   int                    `json:"max_receipts"`
	StateBytes    int64                  `json:"state_bytes"`
	MaxStateBytes int64                  `json:"max_state_bytes"`
	Hours         float64                `json:"receipt_hours"` // how long a receipt lasts
	Observers     []StorageObserverUsage `json:"observers"`
}

// StorageObserverUsage is one observer's share of a store: all of it when the
// store is told of no observers, which Observer then leaves empty.
type StorageObserverUsage struct {
	Observer     string `json:"observer"`
	Receipts     int    `json:"receipts"`
	ReceiptLimit int    `json:"receipt_limit"`
	Bytes        int64  `json:"bytes"` // its receipts, rows and floors, or all state
	ByteLimit    int64  `json:"byte_limit"`
	LastHour     int    `json:"last_hour_receipts"` // receipts taken in the last hour
	Settles      int    `json:"settled_receipts"`   // receipts held at the last hour's rate
	// FullIn is how long, at the last hour's rate, until its receipts fill
	// its share, of receipts or of bytes; Fills is false when they settle
	// within both.
	Fills  bool          `json:"fills"`
	FullIn time.Duration `json:"full_in_ns"`
}
