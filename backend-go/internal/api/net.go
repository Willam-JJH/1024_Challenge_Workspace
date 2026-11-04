package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP 复刻 Python 的 client_ip：默认取 TCP 对端地址；
// 只有开启 behind_cloudflare 且对端属于受信代理时才采信 CF-Connecting-IP。
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	host = strings.Trim(host, "[]")
	if s.cfg.BehindCloudflare && peerTrusted(host, s.trustedProxies) {
		if forwarded := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); forwarded != "" {
			return forwarded
		}
	}
	return host
}

func peerTrusted(host string, networks []netip.Prefix) bool {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, network := range networks {
		if network.Contains(addr) {
			return true
		}
	}
	return false
}

func parsePrefixes(items []string) []netip.Prefix {
	var out []netip.Prefix
	for _, item := range items {
		prefix, err := netip.ParsePrefix(item)
		if err == nil {
			out = append(out, prefix)
		}
	}
	return out
}
