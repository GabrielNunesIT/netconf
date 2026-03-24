package tls

import (
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"net"
	"testing"
)

// syntheticCert builds a minimal *x509.Certificate with the given DER raw
// bytes and identity fields. The Raw field controls the fingerprint; all
// identity fields are optional and default to zero-values.
func syntheticCert(raw []byte, cn string, emails []string, dns []string, ips []net.IP) *x509.Certificate {
	return &x509.Certificate{
		Raw: raw,
		Subject: pkix.Name{
			CommonName: cn,
		},
		EmailAddresses: emails,
		DNSNames:       dns,
		IPAddresses:    ips,
	}
}

// fp returns the SHA-256 fingerprint of raw as a byte slice.
func fp(raw []byte) []byte {
	h := sha256.Sum256(raw)
	return h[:]
}

// TestCertToName_MapTypeSpecified verifies that a fixed username from AuxData
// is returned when MapTypeSpecified matches.
func TestCertToName_MapTypeSpecified(t *testing.T) {
	cert := syntheticCert([]byte("raw-leaf"), "", nil, nil, nil)
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSpecified, AuxData: "admin"},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match, got none")
	}
	if got != "admin" {
		t.Fatalf("expected 'admin', got %q", got)
	}
}

// TestCertToName_MapTypeSANRFC822Name verifies rfc822Name extraction with host
// lowercasing.
func TestCertToName_MapTypeSANRFC822Name(t *testing.T) {
	cert := syntheticCert([]byte("raw-email"), "", []string{"Alice@Example.COM"}, nil, nil)
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANRFC822Name},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match, got none")
	}
	// Local-part preserved; host lowercased.
	if got != "Alice@example.com" {
		t.Fatalf("expected 'Alice@example.com', got %q", got)
	}
}

// TestCertToName_MapTypeSANDNSName verifies dNSName extraction with full
// lowercasing.
func TestCertToName_MapTypeSANDNSName(t *testing.T) {
	cert := syntheticCert([]byte("raw-dns"), "", nil, []string{"Router.Example.Net"}, nil)
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANDNSName},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match, got none")
	}
	if got != "router.example.net" {
		t.Fatalf("expected 'router.example.net', got %q", got)
	}
}

// TestCertToName_MapTypeSANIPAddress_IPv4 verifies IPv4 dotted-quad formatting.
func TestCertToName_MapTypeSANIPAddress_IPv4(t *testing.T) {
	ip := net.ParseIP("192.0.2.1")
	cert := syntheticCert([]byte("raw-ipv4"), "", nil, nil, []net.IP{ip})
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANIPAddress},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match, got none")
	}
	if got != "192.0.2.1" {
		t.Fatalf("expected '192.0.2.1', got %q", got)
	}
}

// TestCertToName_MapTypeSANIPAddress_IPv6 verifies that IPv6 is formatted as
// 32 lowercase hex characters with no colons (RFC 7589 §7 requirement).
func TestCertToName_MapTypeSANIPAddress_IPv6(t *testing.T) {
	ip := net.ParseIP("2001:db8::1")
	cert := syntheticCert([]byte("raw-ipv6"), "", nil, nil, []net.IP{ip})
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANIPAddress},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match, got none")
	}
	// 2001:db8::1 → 20010db8000000000000000000000001
	want := "20010db8000000000000000000000001"
	if got != want {
		t.Fatalf("expected IPv6 hex %q, got %q", want, got)
	}
	if len(got) != 32 {
		t.Fatalf("IPv6 hex must be exactly 32 chars, got %d", len(got))
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Fatalf("IPv6 result is not valid hex: %v", err)
	}
}

// TestCertToName_MapTypeSANIPAddress_IPv6_NoColons explicitly verifies that no
// colon appears in an IPv6-derived username (common pitfall if net.IP.String()
// is used instead of hex.EncodeToString).
func TestCertToName_MapTypeSANIPAddress_IPv6_NoColons(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("::1"),
		net.ParseIP("fe80::1"),
		net.ParseIP("2001:db8:85a3::8a2e:370:7334"),
	}
	wants := []string{
		"00000000000000000000000000000001",
		"fe800000000000000000000000000001",
		"20010db885a3000000008a2e03707334",
	}

	for i, ip := range ips {
		cert := syntheticCert([]byte("raw-ipv6-"+ip.String()), "", nil, nil, []net.IP{ip})
		maps := []MapEntry{
			{Fingerprint: fp(cert.Raw), MapType: MapTypeSANIPAddress},
		}
		got, ok := DeriveUsername(cert, nil, maps)
		if !ok {
			t.Errorf("case %d (%s): expected match", i, ip)
			continue
		}
		if got != wants[i] {
			t.Errorf("case %d (%s): expected %q, got %q", i, ip, wants[i], got)
		}
		for _, ch := range got {
			if ch == ':' {
				t.Errorf("case %d (%s): colon found in IPv6 result %q (must be 32-char hex)", i, ip, got)
			}
		}
	}
}

// TestCertToName_MapTypeSANAny_RFC822First verifies that MapTypeSANAny returns
// the rfc822Name when all three SAN types are present.
func TestCertToName_MapTypeSANAny_RFC822First(t *testing.T) {
	ip := net.ParseIP("10.0.0.1")
	cert := syntheticCert([]byte("raw-any"),
		"cn-name",
		[]string{"user@example.com"},
		[]string{"host.example.com"},
		[]net.IP{ip},
	)
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANAny},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match, got none")
	}
	if got != "user@example.com" {
		t.Fatalf("expected rfc822 to win in MapTypeSANAny, got %q", got)
	}
}

// TestCertToName_MapTypeSANAny_DNSFallback verifies that MapTypeSANAny falls
// through to dNSName when no rfc822Name is present.
func TestCertToName_MapTypeSANAny_DNSFallback(t *testing.T) {
	ip := net.ParseIP("10.0.0.2")
	cert := syntheticCert([]byte("raw-any-dns"),
		"cn-name",
		nil, // no email
		[]string{"device.example.com"},
		[]net.IP{ip},
	)
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANAny},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match, got none")
	}
	if got != "device.example.com" {
		t.Fatalf("expected dns fallback in MapTypeSANAny, got %q", got)
	}
}

// TestCertToName_MapTypeSANAny_IPFallback verifies that MapTypeSANAny falls
// through to iPAddress when neither rfc822Name nor dNSName is present.
func TestCertToName_MapTypeSANAny_IPFallback(t *testing.T) {
	ip := net.ParseIP("198.51.100.5")
	cert := syntheticCert([]byte("raw-any-ip"),
		"cn-name",
		nil, nil, // no email, no dns
		[]net.IP{ip},
	)
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANAny},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match, got none")
	}
	if got != "198.51.100.5" {
		t.Fatalf("expected IP fallback in MapTypeSANAny, got %q", got)
	}
}

// TestCertToName_MapTypeCommonName verifies that the Subject.CommonName is
// returned for MapTypeCommonName.
func TestCertToName_MapTypeCommonName(t *testing.T) {
	cert := syntheticCert([]byte("raw-cn"), "device-router-01", nil, nil, nil)
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeCommonName},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match, got none")
	}
	if got != "device-router-01" {
		t.Fatalf("expected 'device-router-01', got %q", got)
	}
}

// TestCertToName_ExactCertFingerprintMatch explicitly tests that the SHA-256
// fingerprint of cert.Raw drives the exact-cert match path.
func TestCertToName_ExactCertFingerprintMatch(t *testing.T) {
	rawA := []byte("raw-bytes-A")
	rawB := []byte("raw-bytes-B")
	certA := syntheticCert(rawA, "cert-a", nil, nil, nil)
	certB := syntheticCert(rawB, "cert-b", nil, nil, nil)

	// Entry targets certA's fingerprint.
	maps := []MapEntry{
		{Fingerprint: fp(rawA), MapType: MapTypeCommonName},
	}

	// certA should match.
	got, ok := DeriveUsername(certA, nil, maps)
	if !ok || got != "cert-a" {
		t.Fatalf("certA: expected 'cert-a', ok=true; got %q, ok=%v", got, ok)
	}

	// certB must NOT match (different fingerprint).
	got, ok = DeriveUsername(certB, nil, maps)
	if ok {
		t.Fatalf("certB: expected no match, but got %q", got)
	}
}

// TestCertToName_CAChainFingerprintMatch verifies the CA-in-chain fingerprint
// match path: the entry fingerprint identifies a CA cert in verifiedChains,
// and the username is still extracted from the leaf cert.
func TestCertToName_CAChainFingerprintMatch(t *testing.T) {
	leafRaw := []byte("raw-leaf-for-chain")
	caRaw := []byte("raw-ca")

	leaf := syntheticCert(leafRaw, "leaf-device", nil, nil, nil)
	ca := syntheticCert(caRaw, "my-ca", nil, nil, nil)

	// Entry fingerprint matches the CA, not the leaf.
	maps := []MapEntry{
		{Fingerprint: fp(caRaw), MapType: MapTypeCommonName},
	}

	// With CA in verifiedChains, we should get leaf's CommonName.
	verifiedChains := [][]*x509.Certificate{{leaf, ca}}
	got, ok := DeriveUsername(leaf, verifiedChains, maps)
	if !ok {
		t.Fatal("expected CA chain match, got none")
	}
	// Username comes from the leaf cert even though the fingerprint matched the CA.
	if got != "leaf-device" {
		t.Fatalf("expected leaf CN 'leaf-device', got %q", got)
	}
}

// TestCertToName_CAChainMatch_MultipleChains verifies that multiple chains are
// searched exhaustively for the matching CA fingerprint.
func TestCertToName_CAChainMatch_MultipleChains(t *testing.T) {
	leafRaw := []byte("raw-leaf-multi-chain")
	ca1Raw := []byte("raw-ca1")
	ca2Raw := []byte("raw-ca2")

	leaf := syntheticCert(leafRaw, "multi-chain-leaf", nil, nil, nil)
	ca1 := syntheticCert(ca1Raw, "ca1", nil, nil, nil)
	ca2 := syntheticCert(ca2Raw, "ca2", nil, nil, nil)

	// Entry fingerprint matches ca2 (second chain).
	maps := []MapEntry{
		{Fingerprint: fp(ca2Raw), MapType: MapTypeCommonName},
	}

	verifiedChains := [][]*x509.Certificate{
		{leaf, ca1},
		{leaf, ca2},
	}
	got, ok := DeriveUsername(leaf, verifiedChains, maps)
	if !ok {
		t.Fatal("expected match via ca2 in second chain")
	}
	if got != "multi-chain-leaf" {
		t.Fatalf("expected leaf CN 'multi-chain-leaf', got %q", got)
	}
}

// TestCertToName_NoMatch verifies that ("", false) is returned when no entry
// fingerprint matches.
func TestCertToName_NoMatch(t *testing.T) {
	cert := syntheticCert([]byte("raw-no-match"), "nobody", nil, nil, nil)
	wrongFP := fp([]byte("completely-different-raw"))
	maps := []MapEntry{
		{Fingerprint: wrongFP, MapType: MapTypeCommonName},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if ok {
		t.Fatalf("expected no match, but got %q", got)
	}
	if got != "" {
		t.Fatalf("expected empty string on no-match, got %q", got)
	}
}

// TestCertToName_EmptyMaps verifies that an empty map list returns no match.
func TestCertToName_EmptyMaps(t *testing.T) {
	cert := syntheticCert([]byte("raw-empty-maps"), "user", nil, nil, nil)
	got, ok := DeriveUsername(cert, nil, nil)
	if ok {
		t.Fatalf("expected no match with empty maps, got %q", got)
	}
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// TestCertToName_EmptySANList verifies that SAN-based map types return ("", false)
// when no SANs of the required type are present, and the algorithm continues to
// the next entry.
func TestCertToName_EmptySANList(t *testing.T) {
	// Certificate has a CN but no SANs.
	cert := syntheticCert([]byte("raw-no-san"), "fallback-cn", nil, nil, nil)

	maps := []MapEntry{
		// First entry: RFC822 — should yield empty, continue.
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANRFC822Name},
		// Second entry: same fingerprint, CommonName — should yield "fallback-cn".
		{Fingerprint: fp(cert.Raw), MapType: MapTypeCommonName},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match on second entry, got none")
	}
	if got != "fallback-cn" {
		t.Fatalf("expected 'fallback-cn', got %q", got)
	}
}

// TestCertToName_OrderedFirstMatch verifies that the first matching and
// non-empty entry wins (earlier entries take precedence).
func TestCertToName_OrderedFirstMatch(t *testing.T) {
	cert := syntheticCert([]byte("raw-ordered"), "cn-value",
		[]string{"first@example.com"}, nil, nil)

	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANRFC822Name},
		{Fingerprint: fp(cert.Raw), MapType: MapTypeCommonName},
	}

	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match")
	}
	// RFC822 entry comes first and has a value, so it wins.
	if got != "first@example.com" {
		t.Fatalf("expected 'first@example.com' to win, got %q", got)
	}
}

// TestCertToName_SANAny_NoSANs verifies MapTypeSANAny returns no match when
// no SANs of any type are present.
func TestCertToName_SANAny_NoSANs(t *testing.T) {
	cert := syntheticCert([]byte("raw-san-any-empty"), "just-a-cn", nil, nil, nil)
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANAny},
	}
	got, ok := DeriveUsername(cert, nil, maps)
	if ok {
		t.Fatalf("expected no match when no SANs, got %q", got)
	}
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// TestCertToName_RFC822_AlreadyLowercaseHost verifies that a host already in
// lowercase passes through unchanged.
func TestCertToName_RFC822_AlreadyLowercaseHost(t *testing.T) {
	cert := syntheticCert([]byte("raw-rfc822-lower"), "",
		[]string{"user@example.com"}, nil, nil)
	maps := []MapEntry{
		{Fingerprint: fp(cert.Raw), MapType: MapTypeSANRFC822Name},
	}
	got, ok := DeriveUsername(cert, nil, maps)
	if !ok {
		t.Fatal("expected match")
	}
	if got != "user@example.com" {
		t.Fatalf("expected 'user@example.com', got %q", got)
	}
}

// TestCertToName_certFingerprint is a unit test for the internal helper.
func TestCertToName_certFingerprint(t *testing.T) {
	raw := []byte("test-raw-data")
	cert := &x509.Certificate{Raw: raw}
	got := certFingerprint(cert)
	want := sha256.Sum256(raw)
	if string(got) != string(want[:]) {
		t.Fatalf("certFingerprint mismatch: got %x, want %x", got, want)
	}
}
