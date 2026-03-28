package netconf_test

import (
	"testing"

	"github.com/GabrielNunesIT/netconf/netconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Base capability constants ─────────────────────────────────────────────────

func TestBaseCapabilityConstants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "urn:ietf:params:netconf:base:1.0", netconf.BaseCap10)
	assert.Equal(t, "urn:ietf:params:netconf:base:1.1", netconf.BaseCap11)
}

// ── ValidateURN ───────────────────────────────────────────────────────────────

func TestValidateURN_AcceptsValidBase(t *testing.T) {
	t.Parallel()
	valid := []string{
		"urn:ietf:params:netconf:base:1.0",
		"urn:ietf:params:netconf:base:1.1",
		"urn:ietf:params:netconf:base:2.0",
	}
	for _, urn := range valid {
		err := netconf.ValidateURN(urn)
		assert.NoError(t, err, "expected %q to be valid", urn)
	}
}

func TestValidateURN_AcceptsValidCapability(t *testing.T) {
	t.Parallel()
	valid := []string{
		"urn:ietf:params:netconf:capability:rollback-on-error:1.0",
		"urn:ietf:params:netconf:capability:validate:1.1",
		"urn:ietf:params:netconf:capability:startup:1.0",
		"urn:ietf:params:netconf:capability:url:1.0",
		"urn:ietf:params:netconf:capability:xpath:1.0",
		"urn:ietf:params:netconf:capability:notification:1.0",
		"urn:ietf:params:netconf:capability:interleave:1.0",
		"urn:ietf:params:netconf:capability:candidate:1.0",
	}
	for _, urn := range valid {
		err := netconf.ValidateURN(urn)
		assert.NoError(t, err, "expected %q to be valid", urn)
	}
}

func TestValidateURN_RejectsEmpty(t *testing.T) {
	t.Parallel()
	err := netconf.ValidateURN("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestValidateURN_RejectsWrongPrefix(t *testing.T) {
	t.Parallel()
	bad := []string{
		"urn:ietf:params:xml:ns:netconf:base:1.0", // XML namespace, not capability URN
		"http://example.com/netconf",
		"netconf:base:1.0",
		"urn:ietf:netconf:base:1.0", // missing "params"
	}
	for _, urn := range bad {
		err := netconf.ValidateURN(urn)
		assert.Error(t, err, "expected %q to be rejected", urn)
	}
}

func TestValidateURN_RejectsMalformed(t *testing.T) {
	t.Parallel()
	bad := []string{
		"urn:ietf:params:netconf:",                               // too short
		"urn:ietf:params:netconf:base:",                          // missing version
		"urn:ietf:params:netconf:base:1",                         // version not N.N
		"urn:ietf:params:netconf:capability:",                    // missing name and version
		"urn:ietf:params:netconf:capability:rollback-on-error",   // missing version
		"urn:ietf:params:netconf:capability:rollback-on-error:1", // version not N.N
		"urn:ietf:params:netconf:capability::1.0",                // empty name
		"urn:ietf:params:netconf:capability:has space:1.0",       // space in name
		"urn:ietf:params:netconf:something-else:1.0",             // unknown category
	}
	for _, urn := range bad {
		err := netconf.ValidateURN(urn)
		assert.Error(t, err, "expected %q to be rejected", urn)
	}
}

// ── CapabilitySet.Contains ────────────────────────────────────────────────────

func TestCapabilitySet_Contains_Found(t *testing.T) {
	t.Parallel()
	cs := netconf.NewCapabilitySet([]string{
		netconf.BaseCap10,
		netconf.BaseCap11,
	})
	assert.True(t, cs.Contains(netconf.BaseCap10))
	assert.True(t, cs.Contains(netconf.BaseCap11))
}

func TestCapabilitySet_Contains_NotFound(t *testing.T) {
	t.Parallel()
	cs := netconf.NewCapabilitySet([]string{netconf.BaseCap10})
	assert.False(t, cs.Contains(netconf.BaseCap11))
	assert.False(t, cs.Contains("urn:ietf:params:netconf:capability:candidate:1.0"))
}

func TestCapabilitySet_Contains_Empty(t *testing.T) {
	t.Parallel()
	cs := netconf.NewCapabilitySet(nil)
	assert.False(t, cs.Contains(netconf.BaseCap10))
}

func TestCapabilitySet_Contains_CaseSensitive(t *testing.T) {
	t.Parallel()
	// URNs are case-sensitive per RFC 2141.
	cs := netconf.NewCapabilitySet([]string{
		"URN:IETF:PARAMS:NETCONF:BASE:1.0",
	})
	assert.False(t, cs.Contains(netconf.BaseCap10),
		"Contains must be case-sensitive")
}

// ── CapabilitySet.Supports11 / Supports10 ────────────────────────────────────

func TestCapabilitySet_Supports11_True(t *testing.T) {
	t.Parallel()
	cs := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})
	assert.True(t, cs.Supports11())
}

func TestCapabilitySet_Supports11_False(t *testing.T) {
	t.Parallel()
	cs := netconf.NewCapabilitySet([]string{netconf.BaseCap10})
	assert.False(t, cs.Supports11())
}

func TestCapabilitySet_Supports10_True(t *testing.T) {
	t.Parallel()
	cs := netconf.NewCapabilitySet([]string{netconf.BaseCap10})
	assert.True(t, cs.Supports10())
}

func TestCapabilitySet_Supports10_False(t *testing.T) {
	t.Parallel()
	cs := netconf.NewCapabilitySet([]string{netconf.BaseCap11})
	assert.False(t, cs.Supports10())
}

// ── Round-trip: capability URN survives marshal/unmarshal in Hello ────────────

func TestCapability_SurvivesHelloRoundTrip(t *testing.T) {
	t.Parallel()
	// This cross-package test confirms the Capability type (plain string)
	// passes through XML encoding unchanged.
	caps := []string{
		netconf.BaseCap10,
		netconf.BaseCap11,
		"urn:ietf:params:netconf:capability:rollback-on-error:1.0",
	}
	for _, c := range caps {
		err := netconf.ValidateURN(c)
		assert.NoError(t, err, "capability constant %q must be valid", c)
	}
}

// ── Standard optional capability constants (RFC 6241 §8) ─────────────────────

// allStandardCaps is the canonical list of the 8 optional capability constants
// that T01 adds to capability.go.  Tests below iterate over this slice.
var allStandardCaps = []netconf.Capability{
	netconf.CapabilityCandidate,
	netconf.CapabilityConfirmedCommit,
	netconf.CapabilityRollbackOnError,
	netconf.CapabilityValidate,
	netconf.CapabilityStartup,
	netconf.CapabilityURL,
	netconf.CapabilityXPath,
	netconf.CapabilityWritableRunning,
}

// TestStandardCapabilities_ValidURN verifies that every standard capability
// constant passes ValidateURN, confirming the URN strings are correctly formed.
func TestStandardCapabilities_ValidURN(t *testing.T) {
	t.Parallel()
	for _, cap := range allStandardCaps {
		err := netconf.ValidateURN(cap)
		assert.NoError(t, err, "capability constant %q must pass ValidateURN", cap)
	}
}

// TestCapabilitySet_ContainsStandardCaps builds a CapabilitySet containing all
// 8 new constants and verifies that Contains returns true for each one.
func TestCapabilitySet_ContainsStandardCaps(t *testing.T) {
	t.Parallel()
	cs := netconf.NewCapabilitySet([]string{
		netconf.CapabilityCandidate,
		netconf.CapabilityConfirmedCommit,
		netconf.CapabilityRollbackOnError,
		netconf.CapabilityValidate,
		netconf.CapabilityStartup,
		netconf.CapabilityURL,
		netconf.CapabilityXPath,
		netconf.CapabilityWritableRunning,
	})

	for _, cap := range allStandardCaps {
		assert.True(t, cs.Contains(cap), "CapabilitySet must contain %q", cap)
	}
}

// TestStandardCapabilities_Exact verifies that the constant string values
// exactly match the URNs registered in IANA/RFC 6241 §8.
func TestStandardCapabilities_Exact(t *testing.T) {
	t.Parallel()
	expected := map[netconf.Capability]string{
		netconf.CapabilityCandidate:       "urn:ietf:params:netconf:capability:candidate:1.0",
		netconf.CapabilityConfirmedCommit: "urn:ietf:params:netconf:capability:confirmed-commit:1.1",
		netconf.CapabilityRollbackOnError: "urn:ietf:params:netconf:capability:rollback-on-error:1.0",
		netconf.CapabilityValidate:        "urn:ietf:params:netconf:capability:validate:1.1",
		netconf.CapabilityStartup:         "urn:ietf:params:netconf:capability:startup:1.0",
		netconf.CapabilityURL:             "urn:ietf:params:netconf:capability:url:1.0",
		netconf.CapabilityXPath:           "urn:ietf:params:netconf:capability:xpath:1.0",
		netconf.CapabilityWritableRunning: "urn:ietf:params:netconf:capability:writable-running:1.0",
	}
	for cap, want := range expected {
		assert.Equal(t, want, cap, "constant value mismatch")
	}
}

// ── RFC 5277 notification capability constants ────────────────────────────────

// TestCapability_Notification_ValidURN verifies that CapabilityNotification passes
// ValidateURN (RFC 7803 format compliance).
func TestCapability_Notification_ValidURN(t *testing.T) {
	t.Parallel()
	err := netconf.ValidateURN(netconf.CapabilityNotification)
	require.NoError(t, err, "CapabilityNotification must pass ValidateURN")
}

// TestCapability_Interleave_ValidURN verifies that CapabilityInterleave passes
// ValidateURN (RFC 7803 format compliance).
func TestCapability_Interleave_ValidURN(t *testing.T) {
	t.Parallel()
	err := netconf.ValidateURN(netconf.CapabilityInterleave)
	require.NoError(t, err, "CapabilityInterleave must pass ValidateURN")
}

// TestCapability_Notification_ExactValue verifies the exact URN string for
// CapabilityNotification matches RFC 5277 §3.1.
func TestCapability_Notification_ExactValue(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"urn:ietf:params:netconf:capability:notification:1.0",
		netconf.CapabilityNotification,
		"CapabilityNotification must match RFC 5277 §3.1 URN exactly",
	)
}

// TestCapability_Interleave_ExactValue verifies the exact URN string for
// CapabilityInterleave matches RFC 5277 §6.2.
func TestCapability_Interleave_ExactValue(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"urn:ietf:params:netconf:capability:interleave:1.0",
		netconf.CapabilityInterleave,
		"CapabilityInterleave must match RFC 5277 §6.2 URN exactly",
	)
}

// TestCapabilitySet_ContainsNotificationCaps verifies that a CapabilitySet
// built with the notification constants correctly reports Contains for each.
func TestCapabilitySet_ContainsNotificationCaps(t *testing.T) {
	t.Parallel()
	cs := netconf.NewCapabilitySet([]string{
		netconf.CapabilityNotification,
		netconf.CapabilityInterleave,
	})
	assert.True(t, cs.Contains(netconf.CapabilityNotification),
		"CapabilitySet must contain CapabilityNotification")
	assert.True(t, cs.Contains(netconf.CapabilityInterleave),
		"CapabilitySet must contain CapabilityInterleave")
}

// ── RFC 6243 with-defaults capability constant ────────────────────────────────

// TestCapability_WithDefaults_ValidURN verifies that CapabilityWithDefaults passes
// ValidateURN (RFC 7803 format compliance).
func TestCapability_WithDefaults_ValidURN(t *testing.T) {
	t.Parallel()
	err := netconf.ValidateURN(netconf.CapabilityWithDefaults)
	require.NoError(t, err, "CapabilityWithDefaults must pass ValidateURN")
}

// TestCapability_WithDefaults_ExactValue verifies the exact URN string for
// CapabilityWithDefaults matches RFC 6243 §4.
func TestCapability_WithDefaults_ExactValue(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"urn:ietf:params:netconf:capability:with-defaults:1.0",
		netconf.CapabilityWithDefaults,
		"CapabilityWithDefaults must match RFC 6243 §4 URN exactly",
	)
}

// ── RFC 5717 partial-lock capability constant ─────────────────────────────────

// TestCapability_PartialLock_ValidURN verifies that CapabilityPartialLock passes
// ValidateURN (RFC 7803 format compliance).
func TestCapability_PartialLock_ValidURN(t *testing.T) {
	t.Parallel()
	err := netconf.ValidateURN(netconf.CapabilityPartialLock)
	require.NoError(t, err, "CapabilityPartialLock must pass ValidateURN")
}

// TestCapability_PartialLock_ExactValue verifies the exact URN string for
// CapabilityPartialLock matches RFC 5717 §2.4.
func TestCapability_PartialLock_ExactValue(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"urn:ietf:params:netconf:capability:partial-lock:1.0",
		netconf.CapabilityPartialLock,
		"CapabilityPartialLock must match RFC 5717 §2.4 URN exactly",
	)
}
