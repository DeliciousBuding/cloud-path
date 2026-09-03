package pluginhost

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// HandshakeMarker is the fixed first field of a plugin handshake line.
const HandshakeMarker = "CP1"

// Environment variables used to pass the launch identity and expected
// protocol into a plugin process. The plugin must echo these back in its
// handshake line so the host can reject mis-wired or malicious starts.
const (
	EnvPluginID        = "CLOUDPATH_PLUGIN_ID"
	EnvProtocol        = "CLOUDPATH_PROTOCOL"
	EnvProtocolVersion = "CLOUDPATH_PROTOCOL_VERSION"
	EnvLaunchID        = "CLOUDPATH_LAUNCH_ID"
	EnvHandshakeCookie = "CLOUDPATH_HANDSHAKE_COOKIE"
)

// Handshake is the parsed form of a plugin handshake line:
//
//	CP1|<plugin-id>|<protocol>=<version>|<transport>|<endpoint>|<rpc>|<launch-id>|<proof>
type Handshake struct {
	Marker          string
	PluginID        string
	Protocol        string
	ProtocolVersion uint32
	Transport       string
	Endpoint        string
	RPC             string
	LaunchID        string
	Proof           string
}

// String renders the canonical handshake line.
func (h Handshake) String() string {
	marker := h.Marker
	if marker == "" {
		marker = HandshakeMarker
	}
	return strings.Join([]string{
		marker,
		h.PluginID,
		fmt.Sprintf("%s=%d", h.Protocol, h.ProtocolVersion),
		h.Transport,
		h.Endpoint,
		h.RPC,
		h.LaunchID,
		h.Proof,
	}, "|")
}

// IsHandshakeLine reports whether line is a structured handshake line. Any
// such line after the first one is a protocol violation.
func IsHandshakeLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), HandshakeMarker+"|")
}

// ParseHandshake parses and structurally validates a handshake line. It does
// not compare against the expected plugin id, protocol or cookie; that is done
// by Supervisor.validateHandshake.
func ParseHandshake(line string) (Handshake, error) {
	var h Handshake
	parts := strings.Split(strings.TrimSpace(line), "|")
	if len(parts) != 8 {
		return h, fmt.Errorf("handshake: want 8 fields, got %d", len(parts))
	}
	if parts[0] != HandshakeMarker {
		return h, fmt.Errorf("handshake: bad marker %q", parts[0])
	}
	h.Marker = parts[0]
	h.PluginID = parts[1]

	protocol, version, err := parseProtocolDecl(parts[2])
	if err != nil {
		return h, err
	}
	h.Protocol = protocol
	h.ProtocolVersion = version

	h.Transport = parts[3]
	switch h.Transport {
	case "tcp", "unix", "bufconn":
	default:
		return h, fmt.Errorf("handshake: unsupported transport %q", h.Transport)
	}

	h.Endpoint = parts[4]
	if h.Endpoint == "" {
		return h, fmt.Errorf("handshake: empty endpoint")
	}
	if h.Transport == "tcp" {
		if _, _, err := net.SplitHostPort(h.Endpoint); err != nil {
			return h, fmt.Errorf("handshake: invalid tcp endpoint %q: %w", h.Endpoint, err)
		}
	}

	h.RPC = parts[5]
	if h.RPC != "grpc" {
		return h, fmt.Errorf("handshake: unsupported rpc %q", h.RPC)
	}
	h.LaunchID = parts[6]
	if h.LaunchID == "" {
		return h, fmt.Errorf("handshake: empty launch id")
	}
	h.Proof = parts[7]
	if h.Proof == "" {
		return h, fmt.Errorf("handshake: empty proof")
	}
	return h, nil
}

func parseProtocolDecl(field string) (string, uint32, error) {
	k, v, ok := strings.Cut(field, "=")
	if !ok {
		return "", 0, fmt.Errorf("handshake: malformed protocol %q", field)
	}
	switch k {
	case "driver", "application", "connector", "transform":
	default:
		return "", 0, fmt.Errorf("handshake: unsupported protocol %q", k)
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil || n == 0 {
		return "", 0, fmt.Errorf("handshake: malformed protocol version %q", v)
	}
	return k, uint32(n), nil
}
