// Package tls provides TLS transport implementations for NETCONF.
//
// NETCONF over TLS is defined by RFC 7589. This package wraps crypto/tls
// connections as transport.Transport + transport.Upgrader implementations for
// both production use (Dial) and in-process tests (NewClientTransport).
//
// # Cert-to-Name
//
// [DeriveUsername] implements the RFC 7589 §7 certificate-to-name algorithm.
// It maps an authenticated client certificate to a NETCONF username by
// iterating an ordered list of [MapEntry] records. Each entry pairs a SHA-256
// fingerprint with a map type that specifies how the username is extracted.
//
// # Observability
//
// Every error returned by MsgReader, MsgWriter, and Close includes descriptive
// context prefixed with "tls client:" so log lines identify the layer.
// No credentials or secrets pass through this layer after the TLS handshake;
// cert fingerprints and capability URNs are safe to log verbatim.
//
// Failure inspection:
//   - Dial errors name the address and the failed step (dial TCP, TLS handshake).
//   - `go test ./netconf/transport/tls/... -v` prints per-test PASS/FAIL.
package tls

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"strings"
)

// MapType identifies how a username is derived from a certificate when a
// cert-to-name fingerprint match is found. The six values correspond to the
// map types defined in RFC 7589 §7.
type MapType int

const (
	// MapTypeSpecified returns a fixed username string stored in MapEntry.AuxData.
	MapTypeSpecified MapType = iota

	// MapTypeSANRFC822Name derives the username from the first rfc822Name
	// (email address) Subject Alternative Name. The host part is lowercased;
	// the local-part is preserved as-is.
	MapTypeSANRFC822Name

	// MapTypeSANDNSName derives the username from the first dNSName Subject
	// Alternative Name, lowercased.
	MapTypeSANDNSName

	// MapTypeSANIPAddress derives the username from the first iPAddress Subject
	// Alternative Name. IPv4 addresses are formatted as dotted-quad. IPv6
	// addresses are formatted as 32 lowercase hex characters with no colons,
	// per RFC 7589 §7.
	MapTypeSANIPAddress

	// MapTypeSANAny derives the username from the first available SAN in the
	// order: rfc822Name, dNSName, iPAddress. Uses the same formatting rules as
	// the dedicated SAN map types.
	MapTypeSANAny

	// MapTypeCommonName derives the username from cert.Subject.CommonName.
	MapTypeCommonName
)

// MapEntry pairs a SHA-256 certificate fingerprint with a map type that
// specifies how to extract the NETCONF username. AuxData is used only when
// MapType is MapTypeSpecified.
type MapEntry struct {
	// Fingerprint is the SHA-256 hash of the DER-encoded certificate to match.
	// A match is attempted against the leaf certificate first; if that fails,
	// each certificate in each verified chain is checked.
	Fingerprint []byte

	// MapType determines how the username is extracted from the matched
	// certificate.
	MapType MapType

	// AuxData holds the fixed username string when MapType is MapTypeSpecified.
	// It is ignored for all other map types.
	AuxData string
}

// DeriveUsername implements the RFC 7589 §7 certificate-to-name algorithm.
//
// It iterates maps in order. For each entry it first checks whether the
// entry's fingerprint matches the SHA-256 of cert.Raw (exact cert match); if
// not, it scans every certificate in every chain in verifiedChains for a
// fingerprint match (CA-in-chain match).
//
// When a fingerprint match is found the username is extracted according to the
// entry's MapType. If extraction yields a non-empty string, that string is
// returned immediately with ok == true. If extraction yields an empty string
// (e.g., no matching SAN exists), the search continues to the next entry.
//
// If no entry produces a username, DeriveUsername returns ("", false).
func DeriveUsername(cert *x509.Certificate, verifiedChains [][]*x509.Certificate, maps []MapEntry) (string, bool) {
	certFP := sha256.Sum256(cert.Raw)

	for _, entry := range maps {
		// Determine which certificate matched the fingerprint. We first try the
		// leaf cert; on failure we scan the verified chains for a CA match.
		var matched *x509.Certificate

		if bytes.Equal(certFP[:], entry.Fingerprint) {
			matched = cert
		} else {
		chainLoop:
			for _, chain := range verifiedChains {
				for _, chainCert := range chain {
					fp := sha256.Sum256(chainCert.Raw)
					if bytes.Equal(fp[:], entry.Fingerprint) {
						matched = chainCert
						break chainLoop
					}
				}
			}
		}

		if matched == nil {
			continue
		}

		// Fingerprint matched — extract the username according to map type.
		// The username extraction always operates on the *leaf* cert (cert),
		// not on the matched CA cert, because the leaf cert carries the identity
		// fields (SANs, CN). RFC 7589 §7 is explicit: the fingerprint selects
		// which map rule to apply; the name fields always come from the
		// end-entity certificate.
		username := extractUsername(cert, entry)
		if username != "" {
			return username, true
		}
		// Empty result: continue to next entry per RFC 7589 §7.
	}

	return "", false
}

// extractUsername derives the username from cert according to the map type in
// entry. Returns an empty string if the required SAN is absent.
func extractUsername(cert *x509.Certificate, entry MapEntry) string {
	switch entry.MapType {
	case MapTypeSpecified:
		return entry.AuxData

	case MapTypeSANRFC822Name:
		return firstRFC822Name(cert)

	case MapTypeSANDNSName:
		return firstDNSName(cert)

	case MapTypeSANIPAddress:
		return firstIPAddress(cert)

	case MapTypeSANAny:
		if name := firstRFC822Name(cert); name != "" {
			return name
		}
		if name := firstDNSName(cert); name != "" {
			return name
		}
		return firstIPAddress(cert)

	case MapTypeCommonName:
		return cert.Subject.CommonName

	default:
		return ""
	}
}

// firstRFC822Name returns the first rfc822Name SAN with the host part
// lowercased. Returns "" if no rfc822Name SAN is present.
func firstRFC822Name(cert *x509.Certificate) string {
	if len(cert.EmailAddresses) == 0 {
		return ""
	}
	email := cert.EmailAddresses[0]
	if idx := strings.LastIndex(email, "@"); idx >= 0 {
		return email[:idx] + "@" + strings.ToLower(email[idx+1:])
	}
	// Malformed (no @) — return as-is rather than silently drop.
	return email
}

// firstDNSName returns the first dNSName SAN, lowercased.
// Returns "" if no dNSName SAN is present.
func firstDNSName(cert *x509.Certificate) string {
	if len(cert.DNSNames) == 0 {
		return ""
	}
	return strings.ToLower(cert.DNSNames[0])
}

// firstIPAddress returns the first iPAddress SAN formatted per RFC 7589 §7:
//   - IPv4: standard dotted-quad notation (e.g., "192.0.2.1")
//   - IPv6: 32 lowercase hex characters with no colons
//     (e.g., "20010db8000000000000000000000001")
//
// Returns "" if no iPAddress SAN is present.
func firstIPAddress(cert *x509.Certificate) string {
	if len(cert.IPAddresses) == 0 {
		return ""
	}
	ip := cert.IPAddresses[0]
	if ip4 := ip.To4(); ip4 != nil {
		// IPv4: use standard dotted-quad via net.IP.String().
		return ip4.String()
	}
	// IPv6: 32-char lowercase hex, NOT colon-separated (RFC 7589 §7).
	return hex.EncodeToString(ip.To16())
}

// certFingerprint is a helper used in tests to compute the SHA-256 fingerprint
// of a certificate's raw DER encoding.
func certFingerprint(cert *x509.Certificate) []byte {
	fp := sha256.Sum256(cert.Raw)
	return fp[:]
}
