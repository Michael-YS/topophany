# Memory budgets

`serve` and `worker` accept two independent controls, applied before opening
collector state or recovering a worker outbox:

| Flag | Meaning | Default |
| --- | --- | --- |
| `--memory-limit 128MiB` | Go runtime **soft** target, including heap and runtime bookkeeping; not an RSS ceiling | collector 128 MiB; worker 64 MiB |
| `--memory-max 256MiB` | Kernel cgroup v2 cap, covering the service, its subprocesses and charged filesystem cache, with swap disabled | disabled unless requested; enabled in generated Linux services |

Values are integer bytes or have a `KiB`, `MiB` or `GiB` suffix. `--memory-limit 0`
preserves `GOMEMLIMIT`; an explicitly configured `GOMEMLIMIT` also replaces the
normal default when no soft flag was supplied. Minimum nonzero targets are
32 MiB soft and 64 MiB hard. If only a hard cap is supplied, the soft target is
the smaller of the inherited Go target and 75% of the hard cap. An explicitly
supplied soft target must leave at least 25% of the hard cap for other charges.

The Go runtime may exceed its target when the live data needs more memory. It
must not be presented as a hard cap. See the [Go GC guide](https://go.dev/doc/gc-guide#Memory_limit).
A cgroup cap is enforced by the Linux kernel: if reclaim cannot satisfy an
allocation, the service can be OOM-killed. A hard cap protects the rest of the
machine; it cannot guarantee continued monitoring under an undersized budget.

## Linux services and containers

For a small collector, the supplied and newly generated systemd services use:

```ini
[Service]
MemoryAccounting=true
MemoryMax=256M
MemorySwapMax=0
# Add these flags to the existing topo serve command:
# --memory-limit 128MiB --memory-max 256MiB
```

Workers use `MemoryMax=128M`, `MemorySwapMax=0`, and
`--memory-limit 64MiB --memory-max 128MiB`. These are starting budgets for small
installations, not a capacity guarantee for every topology. Existing units must
be updated explicitly; regenerating a bundle does not change a running service.

`--memory-max` verifies an existing cap in the current cgroup or an ancestor. If
no sufficient cap exists, it can lower the current cgroup's limits only when it
is private to the process, has no child groups, and its control files are
writable. It never modifies a shared or root cgroup, raises a stricter cap, or
silently falls back to the Go target. An unavailable controller or insufficient
permissions fails startup with an actionable error. Configure the systemd unit
or container from outside the process in that case. Merely setting
`Delegate=memory` does not grant permission to edit a service's own `memory.max`.

For an existing container image, set both the container memory and total
memory-plus-swap budget to the same number (for example, Docker
`--memory=256m --memory-swap=256m`) and pass
`--memory-limit 128MiB --memory-max 256MiB` to `topo serve` with its normal
configuration. A writable cgroup mount is unnecessary when the required cap is
already in force. The implementation currently expects a unified cgroup v2
mount at `/sys/fs/cgroup`; it refuses incompatible cgroup namespaces/layouts.

See [systemd memory controls](https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html#MemoryMax=)
and [kernel cgroup v2 memory accounting](https://docs.kernel.org/admin-guide/cgroup-v2.html#memory).
A running process is not moved into a new cgroup: existing memory charges do not
follow such a move, so that would leave an apparently limited process's earlier
allocations outside its advertised cap.

macOS and Windows support the Go target. `--memory-max` currently fails explicitly
on these platforms; it does not provide a kernel hard limit there.

## Retention and recovery

`--state-bytes`, `--rollup-bytes`, `--outbox-bytes`, `--max-history` and
`--max-receipts` bound particular data structures or their encoded size. They are
not interchangeable with process memory limits. Retained observations have
maps, strings and indexes; encoded bytes do not directly predict RSS.

Reduce raw history or rollup budgets when those features are unnecessary. Size
receipt retention from sample rates with `topo capacity` before changing
`--retention` or receipt quotas: they protect deduplication, and too small an
allocation stops acceptance rather than deleting unexpired receipts. Lowering
retention also changes how old a queued observation may be when it arrives.

An old large checkpoint still needs memory to recover. Test a copy of existing
state under the proposed cap before deployment. Streaming checkpoint reads and writes avoid whole-file and whole-state
buffers. SHA-256 fingerprints are stored compactly in memory, and a bounded,
rotating dictionary shares repeated strings. Recovery still needs memory for
the live dataset and individual decoded values. It verifies the original
payload bytes with a second file pass before opening the journal.

## Distinguishing retained data from leaks

Both roles log `memory_since_start` at startup and alongside minute-level I/O
reports. Fields include:

- `heap_live_bytes`: live heap recorded by the most recent completed GC;
- `heap_alloc_bytes`: currently allocated Go heap, including uncollected garbage;
- `runtime_bytes`: Go-managed memory minus released heap pages;
- `gc_cycles`, `goroutines`, `next_gc_bytes`, `soft_limit_bytes`.

Compare these with storage usage, process RSS, the service's `memory.current`
and `memory.peak`, and queue depth. They are different accounting measures;
service usage can include subprocesses and cache that process RSS excludes.
Windows Working Set is resident memory; Private Bytes is committed private
memory and should also be recorded.

Growth while receipt retention and long-term rollups fill is expected. Continued
GC-live-heap growth after data counts and load settle is a leak signal. A single
RSS sample or a peak is insufficient to classify it. Do not force GC repeatedly
in production merely to make the number smaller.

Run the synthetic regression across five simulated days:

```sh
go test ./internal/store -run '^TestCollectorMemoryPlateausAcrossRetentionWindows$' -v
```

Profile a private checkpoint copy without opening production journals:

```sh
TOPO_BENCH_CHECKPOINT=/private/copy/checkpoint.json \
  go test ./internal/store -run '^TestCollectorCheckpointMemory$' -v
TOPO_BENCH_CHECKPOINT=/private/copy/checkpoint.json \
  go test ./internal/store -run '^$' -bench '^BenchmarkCollector' -benchmem
```

The profiler copies the source into a temporary directory and writes only there.
It uses a fixed evaluation clock to retain the input workload. Profiles and
checkpoints can contain private evidence; keep them out of version control.

The native Linux acceptance test creates a private child cgroup and deliberately
allocates unreclaimable memory. Its parent must already enable the memory
controller. Run it only in an isolated test environment:

```sh
TOPO_TEST_CGROUP=/sys/fs/cgroup go test ./internal/memlimit -run '^TestKernelMemoryMax$' -v
```

CI runs this kernel OOM test on Linux. Cross-compilation and filesystem-backed
unit tests do not demonstrate kernel enforcement. The synthetic storage soak
covers a fixed workload and cannot establish that every adapter, client, or
operating-system path is leak-free.

Automatic checkpoints hold at most one detached capture. Observation strings
and diagnostic maps are immutable and shared; row containers, receipt and floor
indexes, and mutable rollups are copied. Size memory limits for this temporary
index/rollup overhead as well as live state. Checkpoint encoding remains streamed.
Workers keep outbox payloads on disk, load only a byte-bounded delivery batch,
and cap index size independently with `--outbox-records` (default 20,000).
