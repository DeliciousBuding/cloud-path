package pluginruntime

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// Endpoint is a validated local transport endpoint. Addr is a host:port for
// tcp and an absolute filesystem path for unix.
type Endpoint struct {
	Scheme string
	Addr   string
}

// String renders the canonical endpoint form (tcp://127.0.0.1:port or
// unix:///absolute/path).
func (e Endpoint) String() string {
	return e.Scheme + "://" + e.Addr
}

// tcpPort returns the numeric TCP port, or -1 when it cannot be parsed.
func (e Endpoint) tcpPort() int {
	_, p, err := net.SplitHostPort(e.Addr)
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return -1
	}
	return n
}

// ParseEndpoint parses and structurally validates a local endpoint. It rejects
// userinfo, query, fragment, unknown schemes, non-loopback TCP hosts and
// relative or escaping Unix paths.
func ParseEndpoint(raw string) (Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, fmt.Errorf("pluginruntime: parse endpoint %q: %w", raw, err)
	}
	if u.User != nil {
		return Endpoint{}, fmt.Errorf("pluginruntime: endpoint %q must not contain userinfo", raw)
	}
	if u.RawQuery != "" {
		return Endpoint{}, fmt.Errorf("pluginruntime: endpoint %q must not contain a query", raw)
	}
	if u.Fragment != "" {
		return Endpoint{}, fmt.Errorf("pluginruntime: endpoint %q must not contain a fragment", raw)
	}
	switch u.Scheme {
	case "tcp":
		return parseTCP(u)
	case "unix":
		return parseUnix(u)
	default:
		return Endpoint{}, fmt.Errorf("pluginruntime: unsupported endpoint scheme %q", u.Scheme)
	}
}

func parseTCP(u *url.URL) (Endpoint, error) {
	if u.Path != "" {
		return Endpoint{}, fmt.Errorf("pluginruntime: tcp endpoint %q must not contain a path", u.String())
	}
	host := u.Host
	if host == "" {
		return Endpoint{}, fmt.Errorf("pluginruntime: tcp endpoint missing host")
	}
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return Endpoint{}, fmt.Errorf("pluginruntime: invalid tcp endpoint %q: %w", host, err)
	}
	if h != "127.0.0.1" {
		return Endpoint{}, fmt.Errorf("pluginruntime: tcp endpoint must bind loopback 127.0.0.1, got %q", h)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 0 || port > 65535 {
		return Endpoint{}, fmt.Errorf("pluginruntime: invalid tcp port %q", p)
	}
	return Endpoint{Scheme: "tcp", Addr: net.JoinHostPort("127.0.0.1", p)}, nil
}

func parseUnix(u *url.URL) (Endpoint, error) {
	if u.Host != "" {
		return Endpoint{}, fmt.Errorf("pluginruntime: unix endpoint %q must not contain a host", u.String())
	}
	p := u.Path
	if p == "" || !path.IsAbs(p) {
		return Endpoint{}, fmt.Errorf("pluginruntime: unix endpoint path must be absolute")
	}
	if path.Clean(p) != p {
		return Endpoint{}, fmt.Errorf("pluginruntime: unix endpoint path must be normalized (no . or .. segments)")
	}
	if strings.ContainsRune(p, 0) {
		return Endpoint{}, fmt.Errorf("pluginruntime: unix endpoint path contains NUL")
	}
	return Endpoint{Scheme: "unix", Addr: p}, nil
}
