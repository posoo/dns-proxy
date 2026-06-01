import Foundation
import NetworkExtension

@MainActor
final class DNSProxyModel: ObservableObject {
    @Published var isEnabled = false
    @Published var isWorking = false
    @Published var serverURL = "http://127.0.0.1:8080"
    @Published var currentServerURL = ""
    @Published var statusText = "Load the current DNS proxy preferences."
    @Published var errorMessage: String?
    @Published var lastServerCheck: String?

    private var extensionBundleIdentifier: String {
        let appBundleIdentifier = Bundle.main.bundleIdentifier ?? "dev.senlin.dnsproxy.client"
        return appBundleIdentifier + ".PacketTunnelExtension"
    }

    func load() async {
        isWorking = true
        defer { isWorking = false }

        do {
            let manager = try await loadManager()
            apply(manager)
        } catch {
            errorMessage = error.localizedDescription
            statusText = "Unable to load DNS proxy preferences."
        }
    }

    func save(serverURL: String) async throws {
        let normalizedURL = try validatedServerURL(serverURL)
        isWorking = true
        defer { isWorking = false }

        let manager = try await loadManager()
        configure(manager, serverURL: normalizedURL, enabled: manager.isEnabled)
        try await save(manager)
        apply(manager)
    }

    func enable(serverURL: String) async throws {
        let normalizedURL = try validatedServerURL(serverURL)
        isWorking = true
        defer { isWorking = false }

        let manager = try await loadManager()
        configure(manager, serverURL: normalizedURL, enabled: true)
        try await save(manager)
        apply(manager)
    }

    func disable() async throws {
        isWorking = true
        defer { isWorking = false }

        let manager = try await loadManager()
        manager.isEnabled = false
        manager.connection.stopVPNTunnel()
        try await save(manager)
        apply(manager)
    }

    func testServer(serverURL: String) async throws {
        let normalizedURL = try validatedServerURL(serverURL)
        isWorking = true
        defer { isWorking = false }

        let healthURL = normalizedURL.appending(path: "healthz")
        var request = URLRequest(url: healthURL)
        request.timeoutInterval = 8

        let (data, response) = try await URLSession.shared.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw DNSProxyClientError.invalidServerResponse
        }
        guard httpResponse.statusCode == 200 else {
            throw DNSProxyClientError.serverHealthFailed(httpResponse.statusCode)
        }

        lastServerCheck = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "OK"
        statusText = "Server health check passed."
    }

    private func configure(_ manager: NETunnelProviderManager, serverURL: URL, enabled: Bool) {
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = extensionBundleIdentifier
        proto.providerConfiguration = [
            "serverURL": serverURL.absoluteString,
            "rawQueryPath": "/api/v1/query/raw"
        ]
        proto.serverAddress = serverURL.host() ?? serverURL.absoluteString

        manager.localizedDescription = "DNS Proxy VPN"
        manager.protocolConfiguration = proto
        manager.isEnabled = enabled
    }

    private func apply(_ manager: NETunnelProviderManager) {
        isEnabled = manager.isEnabled
        if
            let proto = manager.protocolConfiguration as? NETunnelProviderProtocol,
            let value = proto.providerConfiguration?["serverURL"] as? String
        {
            serverURL = value
            currentServerURL = value
        }
        let status: String
        switch manager.connection.status {
        case .connected:
            status = "VPN tunnel connected. DNS requests should route through the proxy."
        case .connecting:
            status = "VPN tunnel is connecting."
        case .disconnecting:
            status = "VPN tunnel is disconnecting."
        case .disconnected:
            status = manager.isEnabled ? "VPN configuration is enabled but not connected." : "VPN configuration is installed but disabled."
        case .invalid:
            status = "VPN configuration is invalid."
        case .reasserting:
            status = "VPN tunnel is reconnecting."
        @unknown default:
            status = "VPN status is unknown."
        }
        statusText = status
    }

    private func loadManager() async throws -> NETunnelProviderManager {
        let managers = try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<[NETunnelProviderManager], Error>) in
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: managers ?? [])
                }
            }
        }
        if let existing = managers.first(where: { $0.localizedDescription == "DNS Proxy VPN" }) {
            return existing
        }
        return NETunnelProviderManager()
    }

    private func save(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.saveToPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.loadFromPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
        if manager.isEnabled {
            try manager.connection.startVPNTunnel()
        }
    }

    private func validatedServerURL(_ value: String) throws -> URL {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), let scheme = url.scheme, let host = url.host(), !host.isEmpty else {
            throw DNSProxyClientError.invalidServerURL
        }
        guard scheme == "http" || scheme == "https" else {
            throw DNSProxyClientError.unsupportedScheme
        }
        return url
    }
}

enum DNSProxyClientError: LocalizedError {
    case invalidServerURL
    case unsupportedScheme
    case invalidServerResponse
    case serverHealthFailed(Int)

    var errorDescription: String? {
        switch self {
        case .invalidServerURL:
            "Enter a valid server URL, such as http://192.0.2.10:8080."
        case .unsupportedScheme:
            "The server URL must use http or https."
        case .invalidServerResponse:
            "The server did not return an HTTP response."
        case .serverHealthFailed(let statusCode):
            "Server health check failed with HTTP \(statusCode)."
        }
    }
}
