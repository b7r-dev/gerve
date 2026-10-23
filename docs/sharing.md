# Sharing gerve on a LAN or tailnet

gerve listens on `127.0.0.1` by default. It is a single-user, read-only tool
with no authentication, so widening the bind is an explicit act.

## LAN

```sh
gerve -a 0.0.0.0 -p 8080 .
```

At startup gerve prints the loopback URL and every reachable LAN URL. The header
gains a **share** button; its dialog lists the current page on each reachable
origin with a QR code and a copy action.

`-a 0.0.0.0` binds every interface, including public Wi-Fi. Prefer a tailnet or
a firewall rule when the machine is not on a trusted network.

## Tailscale and MagicDNS

If the machine has a MagicDNS name, pass it as the public origin:

```sh
gerve -a 0.0.0.0 -p 8080 \
  --share-url http://strowger.mule-bramble.b7r.dev:8080 .
```

`--share-url` only controls the URL shown and encoded in the QR code. It does
not create DNS records, open a firewall, or terminate TLS.

A bare host is treated as `http://<host>:<listen-port>`:

```sh
gerve -a 0.0.0.0 -p 8080 --share-url strowger.mule-bramble.b7r.dev .
```

A full origin keeps its own scheme and default port, which is useful behind a
proxy:

```sh
gerve --share-url https://strowger.mule-bramble.b7r.dev .
```

## Tailscale Serve or another TLS proxy

gerve does not terminate TLS itself. If Tailscale Serve or another proxy
terminates HTTPS and forwards to gerve, bind gerve to loopback and give it the
public HTTPS origin:

```sh
gerve -p 8080 --share-url https://strowger.mule-bramble.b7r.dev .
```

In that setup the share button is still available because `--share-url` was
provided explicitly, even though gerve itself is listening only on loopback.
The proxy must forward to the port gerve is actually listening on.

## The share dialog

- The button appears only when gerve is reachable beyond localhost, or when
  `--share-url` is set.
- Each row is the page you are currently looking at, including its path, query,
  and line anchor.
- QR codes are generated in the browser. No URL or repository content is sent to
  a QR service.
- The public URL can be edited in the dialog. The override is stored in this
  browser's `localStorage`; nothing is written on the server.
- Clearing the field removes the public URL for that browser and leaves the
  detected LAN addresses.

## Access control and risk

There is no authentication, no session, and no per-user permission model. Anyone
who can reach the listening port can read the repository, including its commit
history and working tree.

- Use Tailscale ACLs, a firewall, or a trusted LAN as the access boundary.
- `-a 0.0.0.0` exposes every interface, not just the tailnet address.
- Stop gerve when you are done sharing.
- Do not expose gerve directly to the public internet.

## Troubleshooting

- The phone must be on the same LAN or tailnet as the machine running gerve.
- The hostname must resolve on the phone. MagicDNS names only resolve inside the
  tailnet.
- The port must be reachable through the firewall and any proxy.
- An `https://` origin only works if a proxy is actually serving TLS there.
- If a QR code will not scan, use the copy button instead; the QR is black on
  white in both themes for scanner reliability.
