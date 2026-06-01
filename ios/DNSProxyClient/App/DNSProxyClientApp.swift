import SwiftUI

@main
struct DNSProxyClientApp: App {
    @StateObject private var model = DNSProxyModel()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(model)
        }
    }
}
