package main

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// normalizeShareURL turns the --share-url value into a scheme://host[:port]
// origin. A bare host is assumed to be http on the listen port; a full URL keeps
// its scheme and default port, which is what a TLS-terminating proxy such as
// Tailscale Serve needs.
func normalizeShareURL(raw string, listenPort int) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	hadScheme := strings.Contains(raw, "://")
	if !hadScheme {
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https")
	}
	if u.Opaque != "" || u.Hostname() == "" {
		return "", fmt.Errorf("missing host")
	}
	if u.User != nil {
		return "", fmt.Errorf("credentials are not allowed")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("path is not allowed")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("query and fragment are not allowed")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("missing port after ':'")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid port %q", p)
		}
	}

	// A bare host gets the listen port; a full URL is an origin the user already
	// chose, so its implicit default port is left alone.
	if !hadScheme && u.Port() == "" && listenPort != 80 {
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(listenPort))
	}

	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String(), nil
}
