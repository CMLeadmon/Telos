package main

import "strings"

// coverEgressAllowedHosts is the hostname allowlist for candidate-cover
// downloads. It mirrors the cover-relevant subset of
// config/egress/allowed-domains.txt (the Squid proxy enforces the same set at
// the network layer). Egress to any other host is refused before a connection
// opens, in addition to the existing public-address-only SSRF validation.
var coverEgressAllowedHosts = map[string]struct{}{
	"openlibrary.org":             {},
	"covers.openlibrary.org":      {},
	"books.google.com":            {},
	"books.googleusercontent.com": {},
	"image.tmdb.org":              {},
}

// coverHostAllowed reports whether host (case-insensitive, port stripped) is on
// the cover egress allowlist. An exact match or a subdomain of an allowlisted
// apex is permitted; everything else is denied.
func coverHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	if host == "" {
		return false
	}
	if _, ok := coverEgressAllowedHosts[host]; ok {
		return true
	}
	for allowed := range coverEgressAllowedHosts {
		if strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}
