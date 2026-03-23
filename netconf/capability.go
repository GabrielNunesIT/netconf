// Package netconf implements the NETCONF protocol (RFC 6241).
//
// This file defines the Capability type, RFC 7803 URN validation, base
// capability constants, and the CapabilitySet helper type.
package netconf

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Capability is a NETCONF capability URN string.
//
// Capability URNs follow the IANA registry format defined in RFC 7803:
//
//	urn:ietf:params:netconf:base:<version>
//	urn:ietf:params:netconf:capability:<name>:<version>
type Capability = string

// Base capability constants — the two NETCONF framing versions.
const (
	// BaseCap10 is the base:1.0 capability (RFC 6241 EOM framing).
	BaseCap10 Capability = "urn:ietf:params:netconf:base:1.0"

	// BaseCap11 is the base:1.1 capability (RFC 6242 chunked framing).
	BaseCap11 Capability = "urn:ietf:params:netconf:base:1.1"
)

// Standard optional capability constants defined in RFC 6241 §8 and related RFCs.
// All URNs pass ValidateURN and follow the RFC 7803 urn:ietf:params:netconf:capability format.
const (
	// CapabilityCandidate is the :candidate capability (RFC 6241 §8.3).
	// When present, the device supports a candidate configuration datastore.
	CapabilityCandidate Capability = "urn:ietf:params:netconf:capability:candidate:1.0"

	// CapabilityConfirmedCommit is the :confirmed-commit:1.1 capability (RFC 6241 §8.4).
	// When present, the device supports confirmed-commit with a rollback timeout.
	CapabilityConfirmedCommit Capability = "urn:ietf:params:netconf:capability:confirmed-commit:1.1"

	// CapabilityRollbackOnError is the :rollback-on-error capability (RFC 6241 §8.5).
	// When present, the error-option "rollback-on-error" is available for edit-config.
	CapabilityRollbackOnError Capability = "urn:ietf:params:netconf:capability:rollback-on-error:1.0"

	// CapabilityValidate is the :validate:1.1 capability (RFC 6241 §8.6).
	// When present, the device supports the <validate> operation.
	CapabilityValidate Capability = "urn:ietf:params:netconf:capability:validate:1.1"

	// CapabilityStartup is the :startup capability (RFC 6241 §8.7).
	// When present, the device supports a separate startup configuration datastore.
	CapabilityStartup Capability = "urn:ietf:params:netconf:capability:startup:1.0"

	// CapabilityURL is the :url capability (RFC 6241 §8.8).
	// When present, the device supports specifying a URL as a configuration source/target.
	CapabilityURL Capability = "urn:ietf:params:netconf:capability:url:1.0"

	// CapabilityXPath is the :xpath capability (RFC 6241 §8.9).
	// When present, the device supports XPath filter expressions in <get> and <get-config>.
	CapabilityXPath Capability = "urn:ietf:params:netconf:capability:xpath:1.0"

	// CapabilityWritableRunning is the :writable-running capability (RFC 6241 §8.2).
	// When present, the device supports direct writes to the running configuration.
	CapabilityWritableRunning Capability = "urn:ietf:params:netconf:capability:writable-running:1.0"
)

// urnRE matches the two IANA-registered NETCONF URN forms:
//
//	urn:ietf:params:netconf:base:<version>
//	urn:ietf:params:netconf:capability:<name>:<version>
//
// Version and name components allow alphanumeric characters plus dots and
// hyphens as used in published RFCs.
var urnRE = regexp.MustCompile(
	`^urn:ietf:params:netconf:` +
		`(base:[0-9]+\.[0-9]+` +
		`|capability:[a-zA-Z0-9][a-zA-Z0-9._-]*:[0-9]+\.[0-9]+)$`,
)

// ValidateURN checks that s conforms to the RFC 7803 NETCONF capability URN
// format. It returns nil on success and a descriptive error otherwise.
func ValidateURN(s string) error {
	if s == "" {
		return fmt.Errorf("capability URN must not be empty")
	}
	if !strings.HasPrefix(s, "urn:ietf:params:netconf:") {
		return fmt.Errorf("capability URN %q does not start with urn:ietf:params:netconf:", s)
	}
	if !urnRE.MatchString(s) {
		return fmt.Errorf("capability URN %q does not match RFC 7803 format "+
			"(expected urn:ietf:params:netconf:base:<v> or "+
			"urn:ietf:params:netconf:capability:<name>:<v>)", s)
	}
	return nil
}

// CapabilitySet is an ordered list of capability URNs.
type CapabilitySet []Capability

// NewCapabilitySet creates a CapabilitySet from a string slice.
func NewCapabilitySet(caps []string) CapabilitySet {
	return CapabilitySet(caps)
}

// Contains reports whether the set contains the given capability URN.
// The comparison is case-sensitive (URNs are case-sensitive per RFC 2141).
func (cs CapabilitySet) Contains(cap Capability) bool {
	return slices.Contains(cs, cap)
}

// Supports11 reports whether the capability set includes base:1.1.
// This is used during hello exchange to determine whether chunked framing
// should be negotiated.
func (cs CapabilitySet) Supports11() bool {
	return cs.Contains(BaseCap11)
}

// Supports10 reports whether the capability set includes base:1.0.
// RFC 6241 requires all NETCONF implementations to support base:1.0.
func (cs CapabilitySet) Supports10() bool {
	return cs.Contains(BaseCap10)
}
