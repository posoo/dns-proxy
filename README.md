# dns-proxy

A client/server DNS proxy suite for querying hostnames remotely. This repository currently implements the Go server and an initial iOS Packet Tunnel client.

The first iOS client scaffold lives in `ios/` and uses a VPN-style Packet Tunnel Network Extension for DNS interception on personal devices.

## Server

The server accepts DNS-over-HTTP proxy requests from clients, resolves the DNS query from the server, and returns a normal DNS wire response. It supports allowlisted UDP, TCP, DoT, and DoH upstream resolvers, response caching, optional SQLite query logging, and a Basic-auth protected admin surface.

The iOS client uses the raw endpoint transparently by base64-encoding intercepted DNS packets. If the client omits `resolver_name`, the server resolves through the host system resolver from `/etc/resolv.conf`.

### API

`POST /api/v1/query/raw`

```json
{
  "resolver_name": "cloudflare-udp",
  "dns_message": "base64 DNS wire request"
}
```

`POST /api/v1/query/lookup`

```json
{
  "resolver_name": "cloudflare-doh",
  "hostname": "example.com",
  "type": "A",
  "class": "IN"
}
```

Omit `resolver_name` to use the server system default resolver from `/etc/resolv.conf`. Query logs record this as `system-default` with the concrete upstream resolver address used for the exchange.

`GET /healthz` returns a lightweight health check.

Admin pages and JSON endpoints live under `/admin` and use HTTP Basic auth from the YAML config.

### Run Locally

```sh
go run ./cmd/dns-proxy-server -config config.example.yaml
```

Open `http://localhost:8080/admin` and sign in with the configured admin credentials.

### Docker

```sh
docker build -t dns-proxy-server .
docker run --rm -p 8080:8080 -v "$PWD/data:/data" dns-proxy-server
```

For Docker deployments, set `logging.path` in `config.example.yaml` to a mounted path such as `/data/dns-proxy.sqlite3` if SQLite query history should persist across container restarts. The sample config defaults to a relative `dns-proxy.sqlite3` path so `go run` works locally without creating `/data`.

### Configuration

See `config.example.yaml`. Resolvers are allowlisted by name and support:

- `udp` and `tcp` with `address: "host:port"`
- `dot` with `address: "host:port"` and optional `server_name`
- `doh` with `url: "https://..."`

Clients select a configured resolver by `resolver_name`; arbitrary client-supplied resolver addresses are intentionally not supported in v1. If `resolver_name` is omitted, the server uses its system default resolver.

## iOS Client

The iOS app lives in `ios/` and uses a VPN-style Packet Tunnel Network Extension for personal devices. It installs a DNS-only tunnel, captures UDP DNS packets, sends the DNS wire payload to `POST /api/v1/query/raw`, and writes the server response back to iOS.

Use these bundle IDs unless you intentionally rename the app:

- App: `dev.senlin.dnsproxy.client`
- Extension: `dev.senlin.dnsproxy.client.PacketTunnelExtension`

Both targets need the Network Extensions capability with `packet-tunnel-provider`, and provisioning profiles must be regenerated after enabling the capability. See `ios/README.md` for device testing notes, including using the server's LAN/public URL instead of `127.0.0.1` from the phone.

### Tests

```sh
go test ./...
```
