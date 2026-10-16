# RunCat Neo summary

This macOS example publishes a compact Topophany card for RunCat Neo Custom
Metrics. It uses Python 3's standard library and an authenticated `topo` client.

The card has five metric rows: status, hosts online, healthy applications,
check results, and unknown checks (with paused/maintenance counts when present).
Hosts and applications each have a progress bar showing the healthy fraction.
An empty group has no percentage bar. RunCat adds its own title and update footer.
Individual host resources and incident details remain in Topophany.

After configuring a saved client profile with `topo login`, run:

```sh
python3 examples/runcat/export.py \
  --output "$HOME/Library/Application Support/topophany/runcat.json"
```

In RunCat, add that JSON file under **Metrics → Custom Metrics**. Keep the script
running, or use a launch agent with absolute paths to Python, the script and the
`topo` executable (`--topo /path/to/topo`). It refreshes every ten seconds.

Optional arguments:

- `--credentials FILE` and `--profile NAME`: select saved client credentials.
  Without these, `topo` uses its normal credential discovery and active profile.
- `--project ID`: require an expected project in every snapshot.
- `--once`: publish one snapshot and exit; read failures exit with an error.

Rows are capped at 32 display units so long text cannot widen the whole RunCat
dashboard. Expired checks and observers do not count as healthy. The publisher
rejects collector snapshots older than fifteen seconds, writes an unavailable
card on continuous-mode read failures and shutdown, and replaces its output
atomically. Writers targeting the same output share a lock. RunCat's progress
bars use its accent color; they do not encode severity.

Run the focused tests with:

```sh
python3 -m unittest discover -s examples/runcat -p 'test_*.py'
```
