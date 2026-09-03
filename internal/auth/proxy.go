package auth

import (
	"fmt"
	"net"
	"strings"
)

// TrustedProxies 是显式的反向代理来源 allowlist（CIDR 或单 IP）。
// 仅当 TCP 对端地址命中该清单时，才采信 X-Forwarded-For / X-Real-IP /
// X-Forwarded-Proto 等转发头；直连请求伪造这些头一律忽略。
type TrustedProxies struct {
	nets []*net.IPNet
	ips  []net.IP
}

// ParseTrustedProxies 解析 CIDR/IP 清单；空清单返回 nil（不信任任何反代）。
// 任一非法条目返回错误：配置错误必须 fail-closed，绝不静默信任。
func ParseTrustedProxies(cidrs []string) (*TrustedProxies, error) {
	if len(cidrs) == 0 {
		return nil, nil
	}
	tp := &TrustedProxies{}
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if ip := net.ParseIP(raw); ip != nil {
			tp.ips = append(tp.ips, ip)
			continue
		}
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("auth: invalid trusted proxy %q: %w", raw, err)
		}
		tp.nets = append(tp.nets, n)
	}
	if len(tp.nets) == 0 && len(tp.ips) == 0 {
		return nil, nil
	}
	return tp, nil
}

// Empty 报告 allowlist 是否为空（nil 视为空）。
func (tp *TrustedProxies) Empty() bool {
	return tp == nil || (len(tp.nets) == 0 && len(tp.ips) == 0)
}

// Contains 报告 ip 是否命中 allowlist。
func (tp *TrustedProxies) Contains(ip net.IP) bool {
	if tp == nil || ip == nil {
		return false
	}
	for _, a := range tp.ips {
		if a.Equal(ip) {
			return true
		}
	}
	for _, n := range tp.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// TrustsRemote 报告 TCP 对端地址是否为可信反代。
func (tp *TrustedProxies) TrustsRemote(remoteAddr string) bool {
	return tp.Contains(remoteIP(remoteAddr))
}

// remoteIP 解析 RemoteAddr 或转发链条目的 IP，去掉端口并剥离 IPv6 方括号。
func remoteIP(addr string) net.IP {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}
