package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP 复刻 Python 的 client_ip：默认取 TCP 对端地址；只有对端属于
// APP_TRUSTED_PROXIES 时才采信转发头（先 CF-Connecting-IP，再从右往左看
// X-Forwarded-For），避免客户端伪造。
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	host = strings.Trim(host, "[]")
	if !peerTrusted(host, s.trustedProxies) {
		return host
	}
	if s.cfg.BehindCloudflare {
		if forwarded := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); forwarded != "" {
			return forwarded
		}
	}
	chain := r.Header.Get("X-Forwarded-For")
	if chain == "" {
		return host
	}
	hops := make([]string, 0, 4)
	for _, part := range strings.Split(chain, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			hops = append(hops, trimmed)
		}
	}
	for index := len(hops) - 1; index >= 0; index-- {
		if !peerTrusted(hops[index], s.trustedProxies) {
			return hops[index]
		}
	}
	if len(hops) > 0 {
		return hops[0]
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
