# DNS Proxy iOS Client

This folder contains the first iOS client for the DNS proxy suite:

- `DNSProxyClient`: SwiftUI containing app for configuration and enable/disable controls.
- `PacketTunnelExtension`: `NEPacketTunnelProvider` extension that exposes a VPN configuration and handles DNS packets.

## How It Works

1. The app saves a `NETunnelProviderManager` configuration with the server URL in `providerConfiguration`.
2. iOS starts the packet tunnel extension as a VPN.
3. The tunnel configures a fake in-tunnel DNS resolver at `198.18.0.1`.
4. iOS sends DNS packets to that resolver; the tunnel reads UDP/53 packets from `packetFlow`.
5. Each DNS wire message is sent to `POST /api/v1/query/raw` as base64 JSON.
6. The extension decodes the server response and writes a UDP DNS response packet back to the tunnel.

The client intentionally omits `resolver_name`, so the server uses its system default resolver unless the client later exposes resolver selection.

The first packet tunnel implementation handles UDP DNS. TCP DNS fallback can be added if real traffic shows it is needed.

## Apple Developer Setup

Manual work is required before installing on a physical device:

1. In Apple Developer and Xcode, enable the Network Extensions capability for the app ID and extension app ID.
2. Select the Packet Tunnel capability for both targets.
3. Use these bundle IDs:
   - `dev.senlin.dnsproxy.client`
   - `dev.senlin.dnsproxy.client.PacketTunnelExtension`
4. Set your `DEVELOPMENT_TEAM` for both targets.
5. Regenerate provisioning profiles after the capability is enabled.

The containing app uses a VPN-style Network Extension because the project targets personal devices, not MDM-managed DNS Proxy deployment.

## Build Check

The project can be syntax/build checked without signing:

```sh
xcodebuild \
  -project ios/DNSProxyClient.xcodeproj \
  -scheme DNSProxyClient \
  -configuration Debug \
  -destination 'generic/platform=iOS' \
  -derivedDataPath .cache/xcode-derived \
  CODE_SIGNING_ALLOWED=NO \
  build
```

Actual DNS interception requires a signed app on a physical device with the Packet Tunnel Network Extension entitlement.

## Device Test

1. Start the server on an address reachable from the iPhone. For local testing, bind the server to `0.0.0.0:8080` or use Docker with `-p 8080:8080`.
2. In the app, use the server's LAN or public URL, such as `http://192.168.1.10:8080`. Do not use `127.0.0.1` from the phone.
3. Tap **Test Server** first, then **Enable** and approve the VPN configuration prompt.
4. Open Safari or another app and visit a hostname that is unlikely to be cached.
5. Check the server admin logs at `/admin/logs` or the query log API. The iOS client sends no `resolver_name`, so entries should show the server system resolver path.
