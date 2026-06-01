import NetworkExtension
import SwiftUI

struct ContentView: View {
    @EnvironmentObject private var model: DNSProxyModel
    @State private var serverURL = ""
    @State private var showingError = false

    var body: some View {
        NavigationStack {
            ZStack {
                MeshGradient(
                    width: 3,
                    height: 3,
                    points: [
                        .init(0, 0), .init(0.5, 0), .init(1, 0),
                        .init(0, 0.5), .init(0.5, 0.45), .init(1, 0.55),
                        .init(0, 1), .init(0.5, 1), .init(1, 1)
                    ],
                    colors: [
                        .blue.opacity(0.6), .cyan.opacity(0.5), .indigo.opacity(0.55),
                        .mint.opacity(0.55), .white.opacity(0.45), .purple.opacity(0.45),
                        .teal.opacity(0.5), .blue.opacity(0.45), .pink.opacity(0.38)
                    ]
                )
                .ignoresSafeArea()

                ScrollView {
                    VStack(spacing: 18) {
                        statusPanel
                        configurationPanel
                        controlsPanel
                        notesPanel
                    }
                    .padding()
                }
            }
            .navigationTitle("DNS Proxy VPN")
            .toolbarTitleDisplayMode(.large)
            .task {
                await model.load()
                serverURL = model.serverURL
            }
            .alert("DNS Proxy Error", isPresented: $showingError) {
                Button("OK", role: .cancel) {}
            } message: {
                Text(model.errorMessage ?? "Unknown error")
            }
        }
    }

    private var statusPanel: some View {
        GlassCard {
            VStack(alignment: .leading, spacing: 14) {
                HStack {
                    Label(model.isEnabled ? "Active" : "Inactive", systemImage: model.isEnabled ? "checkmark.shield.fill" : "shield.slash")
                        .font(.title2.weight(.semibold))
                    Spacer()
                    Circle()
                        .fill(model.isEnabled ? Color.green : Color.secondary)
                        .frame(width: 12, height: 12)
                }

                Text(model.statusText)
                    .font(.callout)
                    .foregroundStyle(.secondary)

                if !model.currentServerURL.isEmpty {
                    Label(model.currentServerURL, systemImage: "server.rack")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }

                if let lastServerCheck = model.lastServerCheck {
                    Label(lastServerCheck, systemImage: "checkmark.circle")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
            }
        }
    }

    private var configurationPanel: some View {
        GlassCard {
            VStack(alignment: .leading, spacing: 12) {
                Label("Server", systemImage: "network")
                    .font(.headline)

                TextField("http://your-server:8080", text: $serverURL)
                    .textContentType(.URL)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .padding(12)
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))

                Text("The VPN tunnel sends raw DNS wire messages to /api/v1/query/raw. Normal web traffic stays outside the tunnel.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var controlsPanel: some View {
        GlassCard {
            VStack(spacing: 12) {
                Button {
                    Task { await perform { try await model.testServer(serverURL: serverURL) } }
                } label: {
                    Label("Test Server", systemImage: "dot.radiowaves.left.and.right")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)

                Button {
                    Task { await perform { try await model.save(serverURL: serverURL) } }
                } label: {
                    Label("Save Configuration", systemImage: "square.and.arrow.down")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)

                HStack(spacing: 12) {
                    Button {
                        Task { await perform { try await model.enable(serverURL: serverURL) } }
                    } label: {
                        Label("Enable", systemImage: "play.fill")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)

                    Button {
                        Task { await perform { try await model.disable() } }
                    } label: {
                        Label("Disable", systemImage: "stop.fill")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                }
            }
            .disabled(model.isWorking)
            .opacity(model.isWorking ? 0.65 : 1)
        }
    }

    private var notesPanel: some View {
        GlassCard {
            VStack(alignment: .leading, spacing: 10) {
                Label("Device Requirement", systemImage: "iphone")
                    .font(.headline)
                Text("This mode uses a Packet Tunnel Network Extension, so iOS shows VPN status while DNS is routed through the proxy. Test on a physical device with Network Extension signing enabled.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func perform(_ operation: @escaping () async throws -> Void) async {
        do {
            try await operation()
        } catch {
            model.errorMessage = error.localizedDescription
            showingError = true
        }
    }
}

struct GlassCard<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        content
            .padding(18)
            .frame(maxWidth: .infinity, alignment: .leading)
            .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 28, style: .continuous))
    }
}
