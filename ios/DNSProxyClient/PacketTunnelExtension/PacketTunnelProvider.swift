import Foundation
import NetworkExtension
import OSLog

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let logger = Logger(subsystem: "dev.senlin.dnsproxy.client", category: "PacketTunnelProvider")
    private let virtualResolverAddress = "198.18.0.1"
    private let tunnelAddress = "198.18.0.2"
    private var proxyClient: DNSProxyHTTPClient?

    override func startTunnel(options: [String: NSObject]? = nil, completionHandler: @escaping (Error?) -> Void) {
        guard
            let providerConfiguration = (protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration,
            let serverURLString = providerConfiguration["serverURL"] as? String,
            let serverURL = URL(string: serverURLString)
        else {
            completionHandler(PacketTunnelError.missingServerURL)
            return
        }

        let rawQueryPath = providerConfiguration["rawQueryPath"] as? String ?? "/api/v1/query/raw"
        proxyClient = DNSProxyHTTPClient(serverURL: serverURL, rawQueryPath: rawQueryPath)

        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: virtualResolverAddress)
        let ipv4 = NEIPv4Settings(addresses: [tunnelAddress], subnetMasks: ["255.255.255.255"])
        ipv4.includedRoutes = [NEIPv4Route(destinationAddress: virtualResolverAddress, subnetMask: "255.255.255.255")]
        settings.ipv4Settings = ipv4

        let dns = NEDNSSettings(servers: [virtualResolverAddress])
        dns.matchDomains = [""]
        settings.dnsSettings = dns
        settings.mtu = 1500

        setTunnelNetworkSettings(settings) { [weak self] error in
            if let error {
                completionHandler(error)
                return
            }
            self?.logger.info("Started packet tunnel DNS proxy for \(serverURL.absoluteString, privacy: .public)")
            self?.readPackets()
            completionHandler(nil)
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        logger.info("Stopping packet tunnel, reason: \(reason.rawValue)")
        proxyClient = nil
        completionHandler()
    }

    private func readPackets() {
        packetFlow.readPackets { [weak self] packets, protocols in
            guard let self else { return }
            for (packet, protocolNumber) in zip(packets, protocols) {
                guard protocolNumber.int32Value == AF_INET else {
                    continue
                }
                handleIPv4Packet(packet)
            }
            readPackets()
        }
    }

    private func handleIPv4Packet(_ packet: Data) {
        guard let request = IPv4UDPDatagram(packet: packet), request.destinationPort == 53 else {
            return
        }
        guard let proxyClient else {
            return
        }

        Task {
            do {
                let dnsResponse = try await proxyClient.query(dnsMessage: request.payload)
                let response = request.responsePacket(payload: dnsResponse)
                packetFlow.writePackets([response], withProtocols: [NSNumber(value: AF_INET)])
            } catch {
                logger.error("DNS proxy query failed: \(error.localizedDescription, privacy: .public)")
            }
        }
    }
}

struct DNSProxyHTTPClient {
    let serverURL: URL
    let rawQueryPath: String
    private let session = URLSession(configuration: .ephemeral)

    func query(dnsMessage: Data) async throws -> Data {
        let endpoint = serverURL.appending(path: rawQueryPath.trimmingCharacters(in: CharacterSet(charactersIn: "/")))
        var request = URLRequest(url: endpoint)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.timeoutInterval = 10

        let body = RawDNSProxyRequest(dnsMessage: dnsMessage.base64EncodedString())
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw PacketTunnelError.invalidHTTPResponse
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            let errorPayload = try? JSONDecoder().decode(DNSProxyErrorPayload.self, from: data)
            throw PacketTunnelError.serverError(errorPayload?.error.message ?? "HTTP \(httpResponse.statusCode)")
        }

        let payload = try JSONDecoder().decode(RawDNSProxyResponse.self, from: data)
        guard let responseData = Data(base64Encoded: payload.dnsMessage) else {
            throw PacketTunnelError.invalidDNSResponse
        }
        return responseData
    }
}

struct IPv4UDPDatagram {
    let sourceAddress: UInt32
    let destinationAddress: UInt32
    let sourcePort: UInt16
    let destinationPort: UInt16
    let payload: Data

    init?(packet: Data) {
        guard packet.count >= 28 else { return nil }
        let version = packet[0] >> 4
        guard version == 4 else { return nil }
        let headerLength = Int(packet[0] & 0x0f) * 4
        guard packet.count >= headerLength + 8, packet[9] == 17 else { return nil }

        let totalLength = Int(packet.u16(at: 2))
        guard totalLength <= packet.count, totalLength >= headerLength + 8 else { return nil }

        sourceAddress = packet.u32(at: 12)
        destinationAddress = packet.u32(at: 16)
        sourcePort = packet.u16(at: headerLength)
        destinationPort = packet.u16(at: headerLength + 2)

        let udpLength = Int(packet.u16(at: headerLength + 4))
        guard udpLength >= 8, headerLength + udpLength <= totalLength else { return nil }
        payload = Data(packet[(headerLength + 8)..<(headerLength + udpLength)])
    }

    func responsePacket(payload: Data) -> Data {
        let ipv4HeaderLength = 20
        let udpLength = 8 + payload.count
        let totalLength = ipv4HeaderLength + udpLength

        var packet = Data(count: totalLength)
        packet[0] = 0x45
        packet[1] = 0
        packet.setU16(UInt16(totalLength), at: 2)
        packet.setU16(0, at: 4)
        packet.setU16(0, at: 6)
        packet[8] = 64
        packet[9] = 17
        packet.setU32(destinationAddress, at: 12)
        packet.setU32(sourceAddress, at: 16)
        packet.setU16(0, at: 10)
        packet.setU16(packet.ipv4Checksum(headerLength: ipv4HeaderLength), at: 10)

        let udpOffset = ipv4HeaderLength
        packet.setU16(destinationPort, at: udpOffset)
        packet.setU16(sourcePort, at: udpOffset + 2)
        packet.setU16(UInt16(udpLength), at: udpOffset + 4)
        packet.setU16(0, at: udpOffset + 6)
        packet.replaceSubrange((udpOffset + 8)..<totalLength, with: payload)
        packet.setU16(packet.udpChecksum(sourceAddress: destinationAddress, destinationAddress: sourceAddress, udpOffset: udpOffset, udpLength: udpLength), at: udpOffset + 6)

        return packet
    }
}

private extension Data {
    func u16(at index: Int) -> UInt16 {
        UInt16(self[index]) << 8 | UInt16(self[index + 1])
    }

    func u32(at index: Int) -> UInt32 {
        UInt32(self[index]) << 24 | UInt32(self[index + 1]) << 16 | UInt32(self[index + 2]) << 8 | UInt32(self[index + 3])
    }

    mutating func setU16(_ value: UInt16, at index: Int) {
        self[index] = UInt8((value >> 8) & 0xff)
        self[index + 1] = UInt8(value & 0xff)
    }

    mutating func setU32(_ value: UInt32, at index: Int) {
        self[index] = UInt8((value >> 24) & 0xff)
        self[index + 1] = UInt8((value >> 16) & 0xff)
        self[index + 2] = UInt8((value >> 8) & 0xff)
        self[index + 3] = UInt8(value & 0xff)
    }

    func ipv4Checksum(headerLength: Int) -> UInt16 {
        internetChecksum(bytes: self[0..<headerLength])
    }

    func udpChecksum(sourceAddress: UInt32, destinationAddress: UInt32, udpOffset: Int, udpLength: Int) -> UInt16 {
        var pseudoHeader = Data()
        pseudoHeader.appendU32(sourceAddress)
        pseudoHeader.appendU32(destinationAddress)
        pseudoHeader.append(0)
        pseudoHeader.append(17)
        pseudoHeader.appendU16(UInt16(udpLength))
        pseudoHeader.append(contentsOf: self[udpOffset..<(udpOffset + udpLength)])
        let checksum = internetChecksum(bytes: pseudoHeader[0..<pseudoHeader.count])
        return checksum == 0 ? 0xffff : checksum
    }

    mutating func appendU16(_ value: UInt16) {
        append(UInt8((value >> 8) & 0xff))
        append(UInt8(value & 0xff))
    }

    mutating func appendU32(_ value: UInt32) {
        append(UInt8((value >> 24) & 0xff))
        append(UInt8((value >> 16) & 0xff))
        append(UInt8((value >> 8) & 0xff))
        append(UInt8(value & 0xff))
    }

    func internetChecksum(bytes: Data.SubSequence) -> UInt16 {
        var sum: UInt32 = 0
        var index = bytes.startIndex
        while index < bytes.endIndex {
            let high = UInt16(bytes[index]) << 8
            let next = bytes.index(after: index)
            let low = next < bytes.endIndex ? UInt16(bytes[next]) : 0
            sum += UInt32(high | low)
            index = next < bytes.endIndex ? bytes.index(after: next) : bytes.endIndex
        }
        while (sum >> 16) != 0 {
            sum = (sum & 0xffff) + (sum >> 16)
        }
        return ~UInt16(sum & 0xffff)
    }
}

struct RawDNSProxyRequest: Encodable {
    let dnsMessage: String

    enum CodingKeys: String, CodingKey {
        case dnsMessage = "dns_message"
    }
}

struct RawDNSProxyResponse: Decodable {
    let dnsMessage: String

    enum CodingKeys: String, CodingKey {
        case dnsMessage = "dns_message"
    }
}

struct DNSProxyErrorPayload: Decodable {
    let error: ServerError

    struct ServerError: Decodable {
        let code: String
        let message: String
    }
}

enum PacketTunnelError: LocalizedError {
    case missingServerURL
    case invalidHTTPResponse
    case serverError(String)
    case invalidDNSResponse

    var errorDescription: String? {
        switch self {
        case .missingServerURL:
            "DNS proxy server URL is missing from provider configuration."
        case .invalidHTTPResponse:
            "The DNS proxy server returned a non-HTTP response."
        case .serverError(let message):
            message
        case .invalidDNSResponse:
            "The DNS proxy server returned an invalid DNS wire response."
        }
    }
}
