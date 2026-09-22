package base

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// ParseShareLink extracts a provider share ID without requesting the supplied URL.
// An explicit extraction code takes precedence over the URL's pwd parameter.
func ParseShareLink(rawURL, code string, hosts ...string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") ||
		u.User != nil || !slices.Contains(hosts, strings.ToLower(u.Hostname())) {
		return "", "", fmt.Errorf("invalid share URL for this provider")
	}
	id := strings.TrimPrefix(u.Path, "/s/")
	if id == u.Path || id == "" || strings.ContainsFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) {
		return "", "", fmt.Errorf("invalid share ID")
	}
	if code == "" {
		code = u.Query().Get("pwd")
	}
	return id, code, nil
}
