package pluginhost

import (
	"errors"
	"fmt"
	"strings"
)

// Kind is the plugin contribution kind from the manifest. It selects which
// protocol client the host builds after the socket handshake: Driver and
// Application map to their SDK clients; Connector is explicitly unsupported
// by the process host.
type Kind uint8

const (
	KindDriver Kind = iota
	KindApplication
	KindConnector
)

// ErrConnectorUnsupported reports that a Connector plugin cannot be launched
// by the process host. It is returned before any process or endpoint is
// created.
var ErrConnectorUnsupported = errors.New("pluginhost: connector plugin kind is not supported")

// String returns the lowercase, canonical kind name used on the wire.
func (k Kind) String() string {
	switch k {
	case KindApplication:
		return "application"
	case KindConnector:
		return "connector"
	default:
		return "driver"
	}
}

// Protocol returns the protocol name the handshake uses for this kind.
func (k Kind) Protocol() string { return k.String() }

// ParseKind parses a manifest kind string case-insensitively. It accepts the
// canonical manifest values ("Driver", "Application", "Connector") and the
// wire protocol names ("driver", "application", "connector").
func ParseKind(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "driver":
		return KindDriver, nil
	case "application":
		return KindApplication, nil
	case "connector":
		return KindConnector, nil
	default:
		return KindDriver, fmt.Errorf("pluginhost: unknown plugin kind %q", s)
	}
}

// kindFromProtocol maps a protocol name to its kind. Unknown names fall back
// to KindDriver; normalized validation still rejects a mismatching pair.
func kindFromProtocol(protocol string) Kind {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "application":
		return KindApplication
	case "connector":
		return KindConnector
	default:
		return KindDriver
	}
}
