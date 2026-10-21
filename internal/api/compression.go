package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"strconv"
	"strings"
	"sync"
)

var snapshotCompressors = sync.Pool{New: func() any {
	z, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return z
}}

// Reuse the compressor's working memory across cache refreshes. The returned
// bytes belong only to the cache; the pooled writer retains no response buffer.
func compressSnapshot(raw []byte) []byte {
	if len(raw) < 1024 {
		return nil
	}
	var buf bytes.Buffer
	z := snapshotCompressors.Get().(*gzip.Writer)
	z.Reset(&buf)
	_, _ = z.Write(raw)
	_ = z.Close()
	z.Reset(io.Discard)
	snapshotCompressors.Put(z)
	if buf.Len() >= len(raw) {
		return nil
	}
	return buf.Bytes()
}

// Explicit gzip rejection takes precedence over a wildcard. Invalid quality
// values never opt a caller into a representation it may not decode.
func acceptsGzip(header string) bool {
	wildcard := false
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		name := strings.ToLower(strings.TrimSpace(fields[0]))
		quality := 1.0
		for _, parameter := range fields[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if strings.EqualFold(key, "q") {
				q, err := strconv.ParseFloat(value, 64)
				if !ok || err != nil || !(q >= 0 && q <= 1) {
					quality = 0
				} else {
					quality = q
				}
			}
		}
		if name == "gzip" {
			return quality > 0
		}
		if name == "*" {
			wildcard = quality > 0
		}
	}
	return wildcard
}
