package localsignal

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/alpacax/alpacon-cli/pkg/cloud"
)

func consoleLink(raw, workspaceURL, baseDomain string) string {
	link := consoleOrigin(raw)
	workspace := consoleOrigin(workspaceURL)
	if link == nil || workspace == nil || link.Scheme != workspace.Scheme {
		return ""
	}
	host := consoleHost(link.Hostname())
	if host == "" {
		return ""
	}
	if host == consoleHost(workspace.Hostname()) && consolePort(link) == consolePort(workspace) {
		return raw
	}
	base := consoleHost(baseDomain)
	workspaceHost := consoleHost(workspace.Hostname())
	workspaceIP, _ := netip.ParseAddr(workspaceHost)
	if base == cloud.DefaultBaseDomain && !workspaceIP.IsValid() && strings.HasSuffix(workspaceHost, "."+base) && (host == base || strings.HasSuffix(host, "."+base)) && consolePort(link) == defaultConsolePort(link.Scheme) {
		return raw
	}
	return ""
}

func consoleOrigin(raw string) *url.URL {
	if len(raw) > 2048 || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.User != nil || consoleHost(u.Hostname()) == "" || defaultConsolePort(u.Scheme) == "" {
		return nil
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil
		}
	}
	return u
}

func consoleHost(host string) string {
	for _, c := range host {
		if c > unicode.MaxASCII {
			return ""
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.String()
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func consolePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		n, _ := strconv.Atoi(port)
		return strconv.Itoa(n)
	}
	return defaultConsolePort(u.Scheme)
}

func defaultConsolePort(scheme string) string {
	switch scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}
