# Tailscale response fixtures

These are synthetic, minimal responses based on the upstream v1.90.0
[ipnstate definitions](https://github.com/tailscale/tailscale/blob/v1.90.0/ipn/ipnstate/ipnstate.go).
They test parser compatibility with those fields, not execution of that release.
All identities, addresses and credentials are fictitious. A newly observed
format should get its own upstream-version directory and regression case.
Never copy AuthURL, user profiles, private hostnames or full status dumps here.
