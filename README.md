# dns-proxy

A client/server DNS proxy suite for querying hostnames remotely. This repository currently implements the server side first.

## Server

The server accepts DNS-over-HTTP proxy requests from clients, resolves the DNS query from the server, and returns a normal DNS wire response. The first client can use the raw endpoint transparently by base64-encoding the intercepted DNS packet.

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
The example config writes logs to `/data/dns-proxy.sqlite3`; change `logging.path` or disable logging for a purely local run if `/data` is not available.

### Docker

```sh
docker build -t dns-proxy-server .
docker run --rm -p 8080:8080 -v "$PWD/data:/data" dns-proxy-server
```

Mount `/data` if SQLite query history should persist across container restarts.

### Configuration

See `config.example.yaml`. Resolvers are allowlisted by name and support:

- `udp` and `tcp` with `address: "host:port"`
- `dot` with `address: "host:port"` and optional `server_name`
- `doh` with `url: "https://..."`

Clients select a configured resolver by `resolver_name`; arbitrary client-supplied resolver addresses are intentionally not supported in v1. If `resolver_name` is omitted, the server uses its system default resolver.

### Tests

```sh
go test ./...
```
