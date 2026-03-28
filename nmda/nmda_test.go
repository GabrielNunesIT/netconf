package nmda_test

import (
	"encoding/xml"
	"testing"

	"github.com/GabrielNunesIT/netconf/nmda"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNmda_NamespaceConstant verifies the NmdaNS and CapabilityURI constants.
func TestNmda_NamespaceConstant(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "urn:ietf:params:xml:ns:yang:ietf-netconf-nmda", nmda.NmdaNS)
	assert.Equal(t, nmda.NmdaNS, nmda.CapabilityURI, "CapabilityURI must equal NmdaNS")
}

// TestNmda_DatastoreConstants verifies the NMDA datastore URN constants.
func TestNmda_DatastoreConstants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "urn:ietf:params:netconf:datastore:running", nmda.DatastoreRunning)
	assert.Equal(t, "urn:ietf:params:netconf:datastore:candidate", nmda.DatastoreCandidate)
	assert.Equal(t, "urn:ietf:params:netconf:datastore:startup", nmda.DatastoreStartup)
	assert.Equal(t, "urn:ietf:params:netconf:datastore:intended", nmda.DatastoreIntended)
	assert.Equal(t, "urn:ietf:params:netconf:datastore:operational", nmda.DatastoreOperational)

	// All five must be distinct.
	all := []string{
		nmda.DatastoreRunning, nmda.DatastoreCandidate, nmda.DatastoreStartup,
		nmda.DatastoreIntended, nmda.DatastoreOperational,
	}
	seen := make(map[string]bool)
	for _, ds := range all {
		assert.False(t, seen[ds], "datastore constant %q must be unique", ds)
		seen[ds] = true
	}
}

// TestGetData_RoundTrip verifies a GetData request with datastore, xpath filter,
// with-origin, and max-depth round-trips correctly.
func TestGetData_RoundTrip(t *testing.T) {
	t.Parallel()
	original := nmda.GetData{
		Datastore:  nmda.DatastoreRef{Name: nmda.DatastoreOperational},
		Filter:     &nmda.Filter{Type: "xpath", Select: "/interfaces/interface[enabled='true']"},
		WithOrigin: &struct{}{},
		MaxDepth:   5,
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled GetData:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `urn:ietf:params:xml:ns:yang:ietf-netconf-nmda`)
	assert.Contains(t, xmlStr, `get-data`)
	assert.Contains(t, xmlStr, nmda.DatastoreOperational)
	assert.Contains(t, xmlStr, `type="xpath"`)
	assert.Contains(t, xmlStr, `with-origin`)
	assert.Contains(t, xmlStr, `<max-depth>5</max-depth>`)

	var decoded nmda.GetData
	require.NoError(t, xml.Unmarshal(b, &decoded))

	assert.Equal(t, nmda.DatastoreOperational, decoded.Datastore.Name)
	require.NotNil(t, decoded.Filter)
	assert.Equal(t, "xpath", decoded.Filter.Type)
	assert.Equal(t, original.Filter.Select, decoded.Filter.Select)
	assert.NotNil(t, decoded.WithOrigin)
	assert.Equal(t, uint32(5), decoded.MaxDepth)
}

// TestGetData_SubtreeFilter verifies that a GetData request with a subtree
// filter emits the filter content as raw XML (not escaped text).
func TestGetData_SubtreeFilter(t *testing.T) {
	t.Parallel()
	filterContent := []byte(`<interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces"/>`)
	req := nmda.GetData{
		Datastore: nmda.DatastoreRef{Name: nmda.DatastoreOperational},
		Filter:    &nmda.Filter{Content: filterContent},
	}

	b, err := xml.Marshal(req)
	require.NoError(t, err)
	t.Logf("marshaled GetData with subtree filter:\n%s", b)

	xmlStr := string(b)
	// Filter content must appear as raw XML, not as escaped entities.
	assert.Contains(t, xmlStr, `<interfaces`, "filter content must be raw XML, not escaped")
	assert.Contains(t, xmlStr, `ietf-interfaces`)
}

// TestEditData_RoundTrip verifies an EditData request with a candidate datastore,
// default-operation, and a config body. Asserts the config body is emitted as
// raw inner XML (not escaped).
func TestEditData_RoundTrip(t *testing.T) {
	t.Parallel()
	configBody := []byte(`<interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces"><interface><name>eth0</name></interface></interfaces>`)

	original := nmda.EditData{
		Datastore:        nmda.DatastoreRef{Name: nmda.DatastoreCandidate},
		DefaultOperation: "merge",
		Config:           configBody,
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled EditData:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `edit-data`)
	assert.Contains(t, xmlStr, nmda.DatastoreCandidate)
	assert.Contains(t, xmlStr, `<default-operation>merge</default-operation>`)
	// Config body must be raw XML, not escaped.
	assert.Contains(t, xmlStr, `<interfaces`, "config body must be raw XML, not escaped")
	assert.Contains(t, xmlStr, `eth0`)

	var decoded nmda.EditData
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, nmda.DatastoreCandidate, decoded.Datastore.Name)
	assert.Equal(t, "merge", decoded.DefaultOperation)
}

// TestDeleteData_RoundTrip verifies DeleteData round-trip.
func TestDeleteData_RoundTrip(t *testing.T) {
	t.Parallel()
	original := nmda.DeleteData{
		Datastore: nmda.DatastoreRef{Name: nmda.DatastoreCandidate},
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled DeleteData:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `delete-data`)
	assert.Contains(t, xmlStr, nmda.DatastoreCandidate)

	var decoded nmda.DeleteData
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, nmda.DatastoreCandidate, decoded.Datastore.Name)
}

// TestCopyData_RoundTrip verifies CopyData with Source=Running, Target=Startup.
func TestCopyData_RoundTrip(t *testing.T) {
	t.Parallel()
	original := nmda.CopyData{
		Source: nmda.DatastoreRef{Name: nmda.DatastoreRunning},
		Target: nmda.DatastoreRef{Name: nmda.DatastoreStartup},
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled CopyData:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `copy-data`)
	assert.Contains(t, xmlStr, `<source>`)
	assert.Contains(t, xmlStr, `<target>`)
	assert.Contains(t, xmlStr, nmda.DatastoreRunning)
	assert.Contains(t, xmlStr, nmda.DatastoreStartup)

	var decoded nmda.CopyData
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, nmda.DatastoreRunning, decoded.Source.Name)
	assert.Equal(t, nmda.DatastoreStartup, decoded.Target.Name)
}

// TestGetData_OmitOptional verifies that a GetData with only the Datastore field
// does not emit optional elements.
func TestGetData_OmitOptional(t *testing.T) {
	t.Parallel()
	req := nmda.GetData{
		Datastore: nmda.DatastoreRef{Name: nmda.DatastoreRunning},
	}

	b, err := xml.Marshal(req)
	require.NoError(t, err)
	xmlStr := string(b)
	t.Logf("marshaled minimal GetData:\n%s", xmlStr)

	assert.NotContains(t, xmlStr, `<filter>`)
	assert.NotContains(t, xmlStr, `<with-origin>`)
	assert.NotContains(t, xmlStr, `<max-depth>`)
}
