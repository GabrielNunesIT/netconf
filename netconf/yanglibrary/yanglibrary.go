// Package yanglibrary implements the ietf-yang-library YANG module (RFC 8525).
//
// It provides Go struct types for the YANG library data model, which allows
// NETCONF clients to discover which YANG modules a server supports, along
// with their revisions, features, deviations, and datastore bindings.
//
// # Namespace
//
// All types in this package use the YANG module namespace
// "urn:ietf:params:xml:ns:yang:ietf-yang-library" (YangLibraryNS).
// This is a YANG module namespace URI, NOT a NETCONF capability URN of the form
// "urn:ietf:params:netconf:capability:…". Do not pass it to netconf.ValidateURN
// (per P020). Use CapabilityURI to announce the capability in a hello exchange.
//
// # Data Model
//
// The YANG library (RFC 8525 §2) defines a /yang-library container with:
//
//   - /yang-library/module-set: named sets of modules and their import-only modules.
//   - /yang-library/datastore: maps each NMDA datastore to a module-set by name.
//   - /yang-library/content-id: opaque string that changes when the library changes.
//
// Each module entry in a module-set includes the module name, revision,
// namespace, schema URI, supported features, deviation modules, and submodules.
//
// # Observability Impact
//
// Types in this package are pure encoding/decoding structs with no runtime state.
// Failure visibility is through XML marshal/unmarshal errors and go test output.
//
//   - go test ./netconf/yanglibrary/... -v — per-struct round-trip pass/fail
//     with actual marshaled XML printed in failure messages via t.Logf.
//   - The YangLibraryNS constant is testable via:
//     assert.Equal(t, yanglibrary.YangLibraryNS, "urn:ietf:params:xml:ns:yang:ietf-yang-library")
package yanglibrary

import "encoding/xml"

// YangLibraryNS is the XML namespace for the ietf-yang-library YANG module
// (RFC 8525). All elements in this package are qualified with this namespace.
const YangLibraryNS = "urn:ietf:params:xml:ns:yang:ietf-yang-library"

// CapabilityURI is the URI a server includes in its <hello> capabilities list
// to advertise support for the ietf-yang-library YANG module (RFC 8525).
//
// Note: this is a YANG module namespace URI, not a
// "urn:ietf:params:netconf:capability:…" URN. Do not pass it to
// netconf.ValidateURN (per P020).
const CapabilityURI = "urn:ietf:params:xml:ns:yang:ietf-yang-library"

// YangLibrary is the top-level /yang-library container (RFC 8525 §2.1).
//
// It aggregates the module sets, datastore-to-module-set mappings, and the
// content identifier.
//
//   - ModuleSets: one or more named sets of YANG modules. Typically a server
//     defines one module-set and binds it to all datastores.
//   - Datastores: maps each NMDA datastore name to a module-set by name.
//   - ContentID: opaque string that changes whenever the library changes.
//     Clients may cache the library and detect changes by polling content-id.
type YangLibrary struct {
	XMLName    xml.Name        `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-library yang-library"`
	ModuleSets []ModuleSet     `xml:"module-set"`
	Datastores []YangDatastore `xml:"datastore"`
	ContentID  string          `xml:"content-id,omitempty"`
}

// ModuleSet is a named set of YANG modules within the YANG library
// (RFC 8525 §2.2).
//
//   - Name:              unique identifier for this module-set.
//   - Modules:           modules that are implemented in this module-set.
//   - ImportOnlyModules: modules that are only used as imports by other modules
//     in this set (not directly implemented).
type ModuleSet struct {
	XMLName           xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-library module-set"`
	Name              string   `xml:"name"`
	Modules           []Module `xml:"module"`
	ImportOnlyModules []Module `xml:"import-only-module"`
}

// Module describes a single YANG module within a module-set (RFC 8525 §2.2.1).
//
//   - Name:       the YANG module name (e.g. "ietf-interfaces").
//   - Revision:   YANG revision date (YYYY-MM-DD format); omitted when absent.
//   - Namespace:  the YANG module namespace URI.
//   - Schema:     URL where the schema can be retrieved; omitted when not available.
//   - Features:   list of YANG features that are enabled in this module.
//   - Deviations: list of deviation modules that modify this module's schema.
//   - Submodules: list of submodules that are part of this module.
type Module struct {
	Name       string      `xml:"name"`
	Revision   string      `xml:"revision,omitempty"`
	Namespace  string      `xml:"namespace,omitempty"`
	Schema     string      `xml:"schema,omitempty"`
	Features   []string    `xml:"feature"`
	Deviations []Deviation `xml:"deviation"`
	Submodules []Submodule `xml:"submodule"`
}

// Deviation describes a module that deviates (modifies) another YANG module
// (RFC 8525 §2.2.1).
//
//   - Name:     the YANG module name of the deviation module.
//   - Revision: revision date of the deviation module; omitted when absent.
type Deviation struct {
	Name     string `xml:"name"`
	Revision string `xml:"revision,omitempty"`
}

// Submodule describes a YANG submodule within its parent module (RFC 8525 §2.2.1).
//
//   - Name:     the YANG submodule name.
//   - Revision: revision date of the submodule; omitted when absent.
//   - Schema:   URL where the submodule schema can be retrieved; omitted when
//     not available.
type Submodule struct {
	Name     string `xml:"name"`
	Revision string `xml:"revision,omitempty"`
	Schema   string `xml:"schema,omitempty"`
}

// YangDatastore maps a NETCONF/NMDA datastore to a module-set by name
// (RFC 8525 §2.3).
//
//   - Name:   the NMDA datastore identity (e.g. the RFC 8342 datastore URN
//     "urn:ietf:params:netconf:datastore:running", or a custom identity).
//   - Schema: the name of the module-set that defines the schema for this datastore.
type YangDatastore struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-library datastore"`
	Name    string   `xml:"name"`
	Schema  string   `xml:"schema"`
}
