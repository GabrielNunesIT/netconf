package nacm_test

import (
	"encoding/xml"
	"testing"

	"github.com/GabrielNunesIT/netconf/netconf/nacm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNacm_NamespaceConstant verifies the NacmNS constant is the correct
// YANG module namespace URI from RFC 8341.
func TestNacm_NamespaceConstant(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "urn:ietf:params:xml:ns:yang:ietf-netconf-acm", nacm.NacmNS)
	assert.Equal(t, nacm.NacmNS, nacm.CapabilityURI, "CapabilityURI must equal NacmNS")
}

// TestNacm_ActionValues verifies the Action constants match RFC 8341 §3.2.6.
func TestNacm_ActionValues(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "permit", nacm.ActionPermit)
	assert.Equal(t, "deny", nacm.ActionDeny)
}

// TestNacm_RuleTypeValues verifies the RuleType constants.
func TestNacm_RuleTypeValues(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "protocol-operation", nacm.RuleTypeProtocolOperation)
	assert.Equal(t, "notification", nacm.RuleTypeNotification)
	assert.Equal(t, "data-node", nacm.RuleTypeDataNode)
}

// TestNacm_MinimalConfig verifies a minimal Nacm config (just EnableNacm=true)
// encodes without error and produces valid XML.
func TestNacm_MinimalConfig(t *testing.T) {
	t.Parallel()
	cfg := nacm.Nacm{
		EnableNacm: true,
	}

	b, err := xml.Marshal(cfg)
	require.NoError(t, err)
	t.Logf("marshaled minimal nacm:\n%s", b)

	xml := string(b)
	assert.Contains(t, xml, `urn:ietf:params:xml:ns:yang:ietf-netconf-acm`)
	assert.Contains(t, xml, `<enable-nacm>true</enable-nacm>`)
}

// TestNacm_RoundTrip constructs a full Nacm value with groups, two rule-lists,
// and a mix of protocol-operation and notification rules with both permit and
// deny actions. It marshals the value to XML, checks key structural properties,
// then unmarshals back and asserts the result is equal to the original.
func TestNacm_RoundTrip(t *testing.T) {
	t.Parallel()
	original := nacm.Nacm{
		EnableNacm:           true,
		ReadDefault:          nacm.ActionPermit,
		WriteDefault:         nacm.ActionDeny,
		ExecDefault:          nacm.ActionDeny,
		EnableExternalGroups: true,
		Groups: &nacm.Groups{
			Group: []nacm.Group{
				{
					Name:     "admin",
					UserName: []string{"alice", "bob"},
				},
				{
					Name:     "operators",
					UserName: []string{"charlie"},
				},
			},
		},
		RuleLists: []nacm.RuleList{
			{
				Name:  "admin-rules",
				Group: []string{"admin"},
				Rules: []nacm.Rule{
					{
						Name:       "allow-get",
						ModuleName: "ietf-netconf",
						ProtocolOperation: &nacm.ProtocolOperationRule{
							RPCName: "get",
						},
						AccessOperations: "exec",
						Action:           nacm.ActionPermit,
						Comment:          "Admins can get",
					},
					{
						Name:       "allow-get-config",
						ModuleName: "*",
						ProtocolOperation: &nacm.ProtocolOperationRule{
							RPCName: "get-config",
						},
						AccessOperations: "exec",
						Action:           nacm.ActionPermit,
					},
					{
						Name:       "allow-config-change-notification",
						ModuleName: "ietf-netconf-notifications",
						Notification: &nacm.NotificationRule{
							NotificationName: "netconf-config-change",
						},
						AccessOperations: "read",
						Action:           nacm.ActionPermit,
					},
				},
			},
			{
				Name:  "operator-rules",
				Group: []string{"operators"},
				Rules: []nacm.Rule{
					{
						Name:       "deny-edit-config",
						ModuleName: "*",
						ProtocolOperation: &nacm.ProtocolOperationRule{
							RPCName: "edit-config",
						},
						AccessOperations: "exec",
						Action:           nacm.ActionDeny,
					},
					{
						Name:             "deny-all-notifications",
						ModuleName:       "*",
						Notification:     &nacm.NotificationRule{NotificationName: "*"},
						AccessOperations: "*",
						Action:           nacm.ActionDeny,
					},
				},
			},
		},
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled full nacm:\n%s", b)

	xmlStr := string(b)

	// Namespace must appear.
	assert.Contains(t, xmlStr, `urn:ietf:params:xml:ns:yang:ietf-netconf-acm`)

	// Key structural elements.
	assert.Contains(t, xmlStr, `<enable-nacm>true</enable-nacm>`)
	assert.Contains(t, xmlStr, `<read-default>permit</read-default>`)
	assert.Contains(t, xmlStr, `<write-default>deny</write-default>`)
	assert.Contains(t, xmlStr, `<exec-default>deny</exec-default>`)
	assert.Contains(t, xmlStr, `<enable-external-groups>true</enable-external-groups>`)

	// Groups.
	assert.Contains(t, xmlStr, `<name>admin</name>`)
	assert.Contains(t, xmlStr, `<user-name>alice</user-name>`)
	assert.Contains(t, xmlStr, `<user-name>bob</user-name>`)
	assert.Contains(t, xmlStr, `<name>operators</name>`)
	assert.Contains(t, xmlStr, `<user-name>charlie</user-name>`)

	// Rule-lists.
	assert.Contains(t, xmlStr, `<name>admin-rules</name>`)
	assert.Contains(t, xmlStr, `<name>operator-rules</name>`)

	// Protocol-operation rules.
	assert.Contains(t, xmlStr, `<rpc-name>get</rpc-name>`)
	assert.Contains(t, xmlStr, `<rpc-name>get-config</rpc-name>`)
	assert.Contains(t, xmlStr, `<rpc-name>edit-config</rpc-name>`)

	// Notification rules.
	assert.Contains(t, xmlStr, `<notification-name>netconf-config-change</notification-name>`)
	assert.Contains(t, xmlStr, `<notification-name>*</notification-name>`)

	// Actions.
	assert.Contains(t, xmlStr, `<action>permit</action>`)
	assert.Contains(t, xmlStr, `<action>deny</action>`)

	// Now unmarshal back and compare.
	var decoded nacm.Nacm
	err = xml.Unmarshal(b, &decoded)
	require.NoError(t, err)

	// Top-level fields.
	assert.Equal(t, original.EnableNacm, decoded.EnableNacm)
	assert.Equal(t, original.ReadDefault, decoded.ReadDefault)
	assert.Equal(t, original.WriteDefault, decoded.WriteDefault)
	assert.Equal(t, original.ExecDefault, decoded.ExecDefault)
	assert.Equal(t, original.EnableExternalGroups, decoded.EnableExternalGroups)

	// Groups.
	require.NotNil(t, decoded.Groups)
	require.Len(t, decoded.Groups.Group, 2)
	assert.Equal(t, "admin", decoded.Groups.Group[0].Name)
	assert.Equal(t, []string{"alice", "bob"}, decoded.Groups.Group[0].UserName)
	assert.Equal(t, "operators", decoded.Groups.Group[1].Name)
	assert.Equal(t, []string{"charlie"}, decoded.Groups.Group[1].UserName)

	// Rule-lists count and order.
	require.Len(t, decoded.RuleLists, 2)
	assert.Equal(t, "admin-rules", decoded.RuleLists[0].Name)
	assert.Equal(t, []string{"admin"}, decoded.RuleLists[0].Group)

	// Rules within first rule-list.
	require.Len(t, decoded.RuleLists[0].Rules, 3)
	r0 := decoded.RuleLists[0].Rules[0]
	assert.Equal(t, "allow-get", r0.Name)
	assert.Equal(t, "ietf-netconf", r0.ModuleName)
	require.NotNil(t, r0.ProtocolOperation)
	assert.Equal(t, "get", r0.ProtocolOperation.RPCName)
	assert.Equal(t, nacm.ActionPermit, r0.Action)
	assert.Equal(t, "Admins can get", r0.Comment)

	r2 := decoded.RuleLists[0].Rules[2]
	assert.Equal(t, "allow-config-change-notification", r2.Name)
	require.NotNil(t, r2.Notification)
	assert.Equal(t, "netconf-config-change", r2.Notification.NotificationName)

	// Rules within second rule-list.
	assert.Equal(t, "operator-rules", decoded.RuleLists[1].Name)
	require.Len(t, decoded.RuleLists[1].Rules, 2)
	dr := decoded.RuleLists[1].Rules[0]
	assert.Equal(t, "deny-edit-config", dr.Name)
	assert.Equal(t, nacm.ActionDeny, dr.Action)
}

// TestNacm_DenyCounters verifies that deny counters round-trip correctly.
func TestNacm_DenyCounters(t *testing.T) {
	t.Parallel()
	cfg := nacm.Nacm{
		EnableNacm:          true,
		DeniedOperations:    5,
		DeniedDataWrites:    3,
		DeniedNotifications: 12,
	}

	b, err := xml.Marshal(cfg)
	require.NoError(t, err)
	t.Logf("marshaled nacm with counters:\n%s", b)

	var decoded nacm.Nacm
	err = xml.Unmarshal(b, &decoded)
	require.NoError(t, err)

	assert.Equal(t, uint32(5), decoded.DeniedOperations)
	assert.Equal(t, uint32(3), decoded.DeniedDataWrites)
	assert.Equal(t, uint32(12), decoded.DeniedNotifications)
}

// TestNacm_OmitEmptyFields verifies that optional fields with zero values are
// omitted from the marshaled XML (prevent spurious empty elements).
func TestNacm_OmitEmptyFields(t *testing.T) {
	t.Parallel()
	cfg := nacm.Nacm{
		EnableNacm: true,
	}

	b, err := xml.Marshal(cfg)
	require.NoError(t, err)
	xmlStr := string(b)
	t.Logf("marshaled sparse nacm:\n%s", xmlStr)

	// These optional elements must not appear when not set.
	assert.NotContains(t, xmlStr, `<read-default>`)
	assert.NotContains(t, xmlStr, `<write-default>`)
	assert.NotContains(t, xmlStr, `<exec-default>`)
	assert.NotContains(t, xmlStr, `<denied-operations>`)
	assert.NotContains(t, xmlStr, `<denied-data-writes>`)
	assert.NotContains(t, xmlStr, `<denied-notifications>`)
	assert.NotContains(t, xmlStr, `<groups>`)
	assert.NotContains(t, xmlStr, `<rule-list>`)
}
