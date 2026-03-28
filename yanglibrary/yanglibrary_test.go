package yanglibrary_test

import (
	"encoding/xml"
	"testing"

	"github.com/GabrielNunesIT/netconf/yanglibrary"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestYangLibrary_NamespaceConstant verifies the namespace and capability URI
// constants match the RFC 8525 YANG module namespace.
func TestYangLibrary_NamespaceConstant(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "urn:ietf:params:xml:ns:yang:ietf-yang-library", yanglibrary.YangLibraryNS)
	assert.Equal(t, yanglibrary.YangLibraryNS, yanglibrary.CapabilityURI,
		"CapabilityURI must equal YangLibraryNS")
}

// TestYangLibrary_RoundTrip constructs a full YangLibrary with two module-sets,
// modules with features/deviations/submodules, a datastore mapping, and a
// content-id. It marshals to XML, asserts key structural properties, then
// unmarshals back and asserts equality.
func TestYangLibrary_RoundTrip(t *testing.T) {
	t.Parallel()
	original := yanglibrary.YangLibrary{
		ContentID: "abc123",
		ModuleSets: []yanglibrary.ModuleSet{
			{
				Name: "core",
				Modules: []yanglibrary.Module{
					{
						Name:      "ietf-interfaces",
						Revision:  "2018-02-20",
						Namespace: "urn:ietf:params:xml:ns:yang:ietf-interfaces",
						Schema:    "https://example.com/ietf-interfaces.yang",
						Features:  []string{"arbitrary-names", "pre-provisioning"},
						Deviations: []yanglibrary.Deviation{
							{Name: "vendor-devs", Revision: "2023-01-01"},
						},
						Submodules: []yanglibrary.Submodule{
							{Name: "ietf-if-common", Revision: "2018-02-20"},
						},
					},
					{
						Name:      "ietf-ip",
						Revision:  "2018-02-22",
						Namespace: "urn:ietf:params:xml:ns:yang:ietf-ip",
					},
				},
				ImportOnlyModules: []yanglibrary.Module{
					{
						Name:      "ietf-yang-types",
						Revision:  "2013-07-15",
						Namespace: "urn:ietf:params:xml:ns:yang:ietf-yang-types",
					},
				},
			},
			{
				Name: "extensions",
				Modules: []yanglibrary.Module{
					{
						Name:      "vendor-acl",
						Namespace: "urn:example:vendor-acl",
					},
				},
			},
		},
		Datastores: []yanglibrary.YangDatastore{
			{Name: "urn:ietf:params:netconf:datastore:running", Schema: "core"},
			{Name: "urn:ietf:params:netconf:datastore:operational", Schema: "core"},
		},
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled YangLibrary:\n%s", b)

	xmlStr := string(b)

	// Namespace must appear.
	assert.Contains(t, xmlStr, `urn:ietf:params:xml:ns:yang:ietf-yang-library`)
	assert.Contains(t, xmlStr, `yang-library`)

	// Content-id.
	assert.Contains(t, xmlStr, `<content-id>abc123</content-id>`)

	// Module-set structure.
	assert.Contains(t, xmlStr, `<name>core</name>`)
	assert.Contains(t, xmlStr, `<name>extensions</name>`)
	assert.Contains(t, xmlStr, `<name>ietf-interfaces</name>`)
	assert.Contains(t, xmlStr, `<revision>2018-02-20</revision>`)
	assert.Contains(t, xmlStr, `<feature>arbitrary-names</feature>`)
	assert.Contains(t, xmlStr, `<feature>pre-provisioning</feature>`)
	assert.Contains(t, xmlStr, `<name>vendor-devs</name>`)    // deviation
	assert.Contains(t, xmlStr, `<name>ietf-if-common</name>`) // submodule
	assert.Contains(t, xmlStr, `import-only-module`)
	assert.Contains(t, xmlStr, `<name>ietf-yang-types</name>`)

	// Datastores.
	assert.Contains(t, xmlStr, `urn:ietf:params:netconf:datastore:running`)
	assert.Contains(t, xmlStr, `urn:ietf:params:netconf:datastore:operational`)

	// Unmarshal and compare.
	var decoded yanglibrary.YangLibrary
	require.NoError(t, xml.Unmarshal(b, &decoded))

	assert.Equal(t, original.ContentID, decoded.ContentID)
	require.Len(t, decoded.ModuleSets, 2)
	assert.Equal(t, "core", decoded.ModuleSets[0].Name)
	require.Len(t, decoded.ModuleSets[0].Modules, 2)

	m0 := decoded.ModuleSets[0].Modules[0]
	assert.Equal(t, "ietf-interfaces", m0.Name)
	assert.Equal(t, "2018-02-20", m0.Revision)
	assert.Equal(t, "urn:ietf:params:xml:ns:yang:ietf-interfaces", m0.Namespace)
	assert.Equal(t, []string{"arbitrary-names", "pre-provisioning"}, m0.Features)
	require.Len(t, m0.Deviations, 1)
	assert.Equal(t, "vendor-devs", m0.Deviations[0].Name)
	assert.Equal(t, "2023-01-01", m0.Deviations[0].Revision)
	require.Len(t, m0.Submodules, 1)
	assert.Equal(t, "ietf-if-common", m0.Submodules[0].Name)

	require.Len(t, decoded.ModuleSets[0].ImportOnlyModules, 1)
	assert.Equal(t, "ietf-yang-types", decoded.ModuleSets[0].ImportOnlyModules[0].Name)

	require.Len(t, decoded.Datastores, 2)
	assert.Equal(t, "urn:ietf:params:netconf:datastore:running", decoded.Datastores[0].Name)
	assert.Equal(t, "core", decoded.Datastores[0].Schema)
}

// TestYangLibrary_MinimalModule verifies that a Module with only Name and
// Namespace marshals correctly and that optional fields are omitted.
func TestYangLibrary_MinimalModule(t *testing.T) {
	t.Parallel()
	lib := yanglibrary.YangLibrary{
		ModuleSets: []yanglibrary.ModuleSet{
			{
				Name: "minimal",
				Modules: []yanglibrary.Module{
					{
						Name:      "ietf-netconf",
						Namespace: "urn:ietf:params:xml:ns:netconf:base:1.0",
					},
				},
			},
		},
	}

	b, err := xml.Marshal(lib)
	require.NoError(t, err)
	t.Logf("marshaled minimal module:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `<name>ietf-netconf</name>`)
	assert.Contains(t, xmlStr, `<namespace>urn:ietf:params:xml:ns:netconf:base:1.0</namespace>`)
	assert.NotContains(t, xmlStr, `<revision>`)
	assert.NotContains(t, xmlStr, `<schema>`)
	assert.NotContains(t, xmlStr, `<feature>`)
	assert.NotContains(t, xmlStr, `<deviation>`)
	assert.NotContains(t, xmlStr, `<submodule>`)
}

// TestYangLibrary_OmitEmptyFields verifies that an empty YangLibrary marshals
// without spurious empty child elements.
func TestYangLibrary_OmitEmptyFields(t *testing.T) {
	t.Parallel()
	lib := yanglibrary.YangLibrary{}

	b, err := xml.Marshal(lib)
	require.NoError(t, err)
	xmlStr := string(b)
	t.Logf("marshaled empty YangLibrary:\n%s", xmlStr)

	assert.NotContains(t, xmlStr, `<content-id>`)
	assert.NotContains(t, xmlStr, `<module-set>`)
	assert.NotContains(t, xmlStr, `<datastore>`)
}
