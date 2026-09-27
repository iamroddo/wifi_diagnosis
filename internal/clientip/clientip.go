// Package clientip extracts the real client IP from an HTTP request.
// It only trusts X-Forwarded-For / Forwarded headers from explicitly
// configured trusted proxies — arbitrary header values are never trusted.
package clientip

import (
	"net"
	"net/http"
	"strings"
)

// FromRequest returns the best-effort real client IP for the request.
// If trustedProxies is non-empty and the direct connection comes from one
// of those proxies, the leftmost untrusted address from X-Forwarded-For
// (or Forwarded) is returned. Otherwise, the address from r.RemoteAddr is used.
func FromRequest(r *http.Request, trustedProxies []net.IP) string {
	directIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr has no port (unusual but handle it)
		directIP = r.RemoteAddr
	}

	if len(trustedProxies) == 0 || !isTrusted(directIP, trustedProxies) {
		return directIP
	}

	// The direct connection is from a trusted proxy; extract the original IP.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For: client, proxy1, proxy2
		// Walk right-to-left, skipping trusted proxies.
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(parts[i])
			if candidate == "" {
				continue
			}
			if !isTrusted(candidate, trustedProxies) {
				return candidate
			}
		}
	}

	if fwd := r.Header.Get("Forwarded"); fwd != "" {
		// Parse the last untrusted "for=" value.
		for _, field := range strings.Split(fwd, ",") {
			for _, part := range strings.Split(strings.TrimSpace(field), ";") {
				part = strings.TrimSpace(part)
				if !strings.HasPrefix(strings.ToLower(part), "for=") {
					continue
				}
				candidate := strings.TrimPrefix(part, "for=")
				candidate = strings.TrimPrefix(part[4:], `"`)
				candidate = strings.TrimSuffix(candidate, `"`)
				// Strip port if present (format: [ip]:port or ip:port)
				if h, _, err := net.SplitHostPort(candidate); err == nil {
					candidate = h
				}
				candidate = strings.Trim(candidate, "[]")
				if !isTrusted(candidate, trustedProxies) {
					return candidate
				}
			}
		}
	}

	// Fall back to the direct connection IP.
	return directIP
}

func isTrusted(ipStr string, trusted []net.IP) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	for _, t := range trusted {
		if t.Equal(ip) {
			return true
		}
	}
	return false
}
