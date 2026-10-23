package direct

import (
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/source"
	"testing"
)

func BenchmarkDirectSourceUpdate(b *testing.B) {
	for _, n := range []int{20, 256} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			s, c, now := fleetSession(b, n, 140)
			src := c.Sources[0]
			var readings []source.Reading
			for _, x := range c.Checks {
				if x.External.Source == src.ID {
					r := resourceReading(now, "999999999999999")
					r.Object = x.External.Object
					r.Report.Object = r.Object
					readings = append(readings, r)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if e := s.Record("audit", src.ID, now, now, readings, nil); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}
