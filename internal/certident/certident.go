// Package certident parses the client certificate identity that
// inbound-gateway forwards via the standard XFCC ("x-forwarded-client-cert")
// header, produced by the ClientTrafficPolicy's headers.xForwardedClientCert
// setting (mode SanitizeSet, certDetailsToAdd: [Subject]; Hash is always
// included automatically). Envoy renders that header as semicolon-separated
// key=value pairs, e.g.:
//
//	Hash=ab12...;Subject="CN=alice"
//
// This package extracts just the two fields callers need: the certificate's
// Subject Common Name (CN) and its SHA-256 fingerprint (Hash).
package certident

import "strings"

// Identity is the parsed client-certificate identity forwarded by the
// gateway. JSON tags match the "client" field services embed in their
// response bodies.
type Identity struct {
	CN          string `json:"cn"`
	Fingerprint string `json:"fingerprint"`
}

// Parse extracts CN and fingerprint (Hash) from a raw XFCC header value.
// ok is false if the header is empty or no CN could be found in the
// Subject field.
func Parse(xfcc string) (Identity, bool) {
	if xfcc == "" {
		return Identity{}, false
	}

	var hash, subject string
	for _, part := range strings.Split(xfcc, ";") {
		key, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		value = strings.Trim(value, `"`)
		switch key {
		case "Hash":
			hash = value
		case "Subject":
			subject = value
		}
	}

	cn := extractCN(subject)
	if cn == "" {
		return Identity{}, false
	}
	return Identity{CN: cn, Fingerprint: hash}, true
}

// extractCN pulls the CN=<value> attribute out of a comma-separated DN
// string such as "CN=alice,O=demo".
func extractCN(subject string) string {
	for _, attr := range strings.Split(subject, ",") {
		key, value, found := strings.Cut(attr, "=")
		if found && key == "CN" {
			return value
		}
	}
	return ""
}
