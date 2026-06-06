# simplewan

A deliberately small two-WAN **failover** daemon for OpenWrt — a focused
alternative to mwan3 when all you want is: *use the primary uplink while it's
healthy, fail over to the backup when it isn't, and switch back after it has
been healthy for a while.*

No load balancing, no per-flow policies, no firewall marks, **no
iptables/nftables**. Just ICMP probes bound to each interface and a reordering
of the IPv4 default routes.

## Safety invariant

simplewan is designed so it can **never cause an outage that wouldn't happen
without it**:

- It only ever *reorders* existing default routes. It never installs a
  blackhole/unreachable route and never removes the last default route.
- If **no** WAN looks healthy (e.g. your single ping target is globally down),
  it does nothing — it holds the last-good selection. Worst case it degrades to
  "no failover", never to "no connectivity".
- The daemon is never in the packet path. If `simplewand` crashes or is killed,
  the routes it last set simply remain, and traffic keeps flowing.

## How it works

1. One ICMP echo target is pinged out of **each** WAN, with the socket bound to
   that WAN's device (`SO_BINDTODEVICE`). Binding is essential: it lets the
   daemon test a WAN that is *not* currently the default route, so a
   demoted-but-recovering link can be observed coming back.
2. Per-WAN hysteresis: a WAN goes offline after `down` consecutive failed
   checks and back online after `up` consecutive good ones.
3. Selection: the lowest-`priority` healthy WAN wins. Failing **over** (the
   current WAN went unhealthy) is immediate. Switching **back** to a
   more-preferred WAN waits until it has been healthy for `recovery_time`
   seconds (anti-flap).
4. The selected WAN keeps its configured `metric`; any WAN that would otherwise
   outrank it is demoted by `demote_offset`. Metrics are applied
   make-before-break (add the new metric, then drop the old) so there is never
   a moment with no default route for a live WAN.
5. A netlink route monitor re-asserts these metrics whenever netifd re-adds a
   default route (DHCP/PPPoE renewals).

Only IPv4 is handled for now.

## Requirements

- Both WANs must be in a masqueraded firewall zone (standard `wan` zone), or
  failover traffic won't be NATed.
- The daemon runs as root (needs raw ICMP sockets); procd handles that.

## Configuration (`/etc/config/simplewan`)

```
config globals 'globals'
	option enabled '1'
	option ping_target '1.1.1.1'   # single IPv4 target
	option interval '5'            # seconds between checks
	option timeout '2'             # per-ping timeout
	option count '1'               # pings per check
	option down '3'                # failed checks -> offline
	option up '3'                  # good checks -> online
	option recovery_time '300'     # healthy seconds before switching back
	option flush_conntrack '1'

config interface 'primary'
	option ifname 'wan'
	option metric '10'
	option priority '1'            # lower = preferred

config interface 'backup'
	option ifname 'wan2'
	option metric '20'
	option priority '2'

config notify 'notify'
	option enabled '0'
	option postmark_token ''
	option mail_from 'router@example.com'
	option mail_to 'you@example.com'
	option subject_prefix '[simplewan]'
```

Set `metric` to match each interface's netifd metric so the resting state needs
no intervention. The config file is installed mode `0600` because it holds the
Postmark token.

## Email notifications (Postmark)

When configured, simplewan emails on every selection change (failover and
switch-back) via the Postmark API. Sending is asynchronous and time-bounded, so
a slow or failing Postmark request can never delay a routing change. `mail_from`
must be a verified Postmark sender.

## LuCI

`luci-app-simplewan` adds a status page (per-WAN health, active upstream, loss,
RTT) and a configuration form under **Network → SimpleWAN Failover**. The status
page reads `/var/run/simplewan/status.json` through a small ucode rpcd backend.

## Building and the feed

The daemon is a static Go binary. Build the packages with an OpenWrt SDK for
your target (Turris Omnia = `mvebu/cortexa9`, package arch
`arm_cortex-a9_vfpv3`):

```
scripts/build-feed.sh /path/to/openwrt-sdk
```

The GitHub Actions workflow (`.github/workflows/feed.yml`) does the same and
publishes a **signed opkg feed** to GitHub Pages on each `v*` tag.

### Signing

The feed index is signed with `usign`. The **public** key is committed at
[`feed/simplewan-feed.pub`](feed/simplewan-feed.pub) (fingerprint
`913328a568e78b46`). The **secret** key lives only in the `USIGN_SECRET`
repository Actions secret — never in the repo or the published site. Whoever
holds that secret can publish packages the router will trust automatically.

### Installing on the router

```
# trust the feed's signing key
echo 'RWSRMyilaOeLRofZWUzeLc9CEK8XGijN6sv5UJ32hIlNX021r7nLPJ7I' \
	> /etc/opkg/keys/913328a568e78b46

# add the feed
echo 'src/gz simplewan https://<user>.github.io/simplewan' \
	>> /etc/opkg/customfeeds.conf

opkg update
opkg install simplewan luci-app-simplewan
```

### Surviving firmware upgrades

`/etc/config/simplewan` is a conffile, so sysupgrade preserves your settings.
The package itself is **not** automatically reinstalled by a plain sysupgrade —
just run `opkg install simplewan luci-app-simplewan` again from the feed after
upgrading (or bake it into a custom image).

## Status

Early. The Go daemon and its logic are tested; the SDK build and the CI feed
workflow have not yet been validated end-to-end on real hardware.
