# Beszel protocol fixtures

`system_stats_v0.21.json` is a synthetic PocketBase list response, not a capture
from a production machine. Its field names and units follow Beszel v0.21.0's
`Stats` and filesystem structures. The values deliberately distinguish
the reported root-disk percentage (`dp: 23.25`) from the ratio of rounded GiB
values (`du / d: 20%`). The adapter must preserve the reported percentage.

The list metadata uses `totalItems: -1` and `totalPages: -1` because the
production latest-sample query explicitly sets `skipTotal=true`; it does not
need to count the system's historical samples.

The 15-character identifiers and timestamp are synthetic. `created` represents
the Hub's creation time for a raw one-minute record; it is not an independently
verified Agent measurement timestamp. The token used by tests is also synthetic.

Protocol sources:

- https://github.com/henrygd/beszel/blob/ec96c134660c998f7eb044e349e3715b65733fe1/internal/entities/system/system.go
- https://pocketbase.io/docs/api-records/
- https://pocketbase.io/docs/api-rules-and-filters/

The tests also construct forward-format `mem: [total, used, buff/cache, zfs_arc]`
fixtures in bytes. In particular, values above 2^53 exercise lossless integer
decoding; they are boundary-test values, not claims about typical RAM sizes.
