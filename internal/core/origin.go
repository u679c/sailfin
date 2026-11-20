package core

import (
	"net/url"
	"strings"
)

func trustedManagementOrigin(configured, origin string) bool {
	if configured == origin {
		return true
	}
	// Local development links may use localhost or either loopback address.
	// Keep the scheme and port fixed and require exact origins for other hosts.
	wantScheme, wantPort, wantLocal := localManagementOrigin(configured)
	scheme, port, local := localManagementOrigin(origin)
	return wantLocal && local && wantScheme == scheme && wantPort == port
}

func localManagementOrigin(raw string) (scheme, port string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil ||
		raw != u.Scheme+"://"+u.Host || strings.HasSuffix(u.Host, ":") {
		return "", "", false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
	default:
		return "", "", false
	}
	port = u.Port()
	switch u.Scheme {
	case "http":
		if port == "" {
			port = "80"
		}
	case "https":
		if port == "" {
			port = "443"
		}
	default:
		return "", "", false
	}
	return u.Scheme, port, true
}
