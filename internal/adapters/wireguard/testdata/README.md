# WireGuard public-field fixtures

Synthetic outputs follow the public `wg show` fields documented by
[wireguard-tools v1.0.20210914](https://git.zx2c4.com/wireguard-tools/tree/src/show.c?h=v1.0.20210914).
They validate parser compatibility, not native execution of that release.
AllowedIPs are space separated after the initial tab; keepalive uses `off`.
Never add dump, private-key or preshared-keys output to these fixtures.
