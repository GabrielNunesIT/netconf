package nacm

import "strings"

// Decision is the result of an NACM enforcement evaluation.
// It indicates whether the requested access is permitted, denied by a
// matching rule, or denied by the default policy (no rule matched).
type Decision int

const (
	// Permit indicates the request is allowed (a matching rule granted access).
	Permit Decision = iota

	// Deny indicates the request is explicitly denied by a matching rule.
	Deny

	// DefaultDeny indicates no rule matched; access is denied by default policy.
	// This is distinct from Deny to allow callers to distinguish rule-based
	// denial from absence-of-rule denial (useful for audit logging).
	DefaultDeny
)

// String returns a human-readable representation of the Decision.
func (d Decision) String() string {
	switch d {
	case Permit:
		return "permit"
	case Deny:
		return "deny"
	case DefaultDeny:
		return "default-deny"
	default:
		return "unknown"
	}
}

// OperationType specifies the category of access being requested.
type OperationType int

const (
	// OpProtocolOperation is an access request for a NETCONF RPC operation.
	// The enforcement algorithm checks for exec access.
	OpProtocolOperation OperationType = iota

	// OpNotification is an access request to deliver a NETCONF notification.
	// The enforcement algorithm checks for read access.
	OpNotification
)

// Request describes a single NACM access control request.
//
//   - User:          the NETCONF username making the request.
//   - Groups:        the groups the user belongs to (from /nacm/groups and
//     optionally from external AAA when enable-external-groups is true).
//   - OperationType: whether this is a protocol-operation or notification request.
//   - OperationName: the local name of the RPC or notification (e.g. "get-config",
//     "netconf-config-change"). Ignored when not relevant to the operation type.
//   - ModuleName:    the YANG module defining the operation (e.g. "ietf-netconf").
//     Use "*" to match rules with a wildcard module.
type Request struct {
	User          string
	Groups        []string
	OperationType OperationType
	OperationName string
	ModuleName    string
}

// Enforce evaluates a NACM access control request against the provided
// configuration and returns a Decision.
//
// The algorithm follows RFC 8341 §3.4:
//
//  1. If cfg.EnableNacm is false, return Permit (NACM is disabled).
//  2. Walk cfg.RuleLists in document order.
//     For each RuleList, check whether the requesting user's groups overlap
//     with the rule-list's Group list (an empty Group list applies to all groups).
//     For each Rule in the list (in order):
//     a. Check that the rule's module-name matches req.ModuleName (or is "*").
//     b. Check that the rule type matches req.OperationType:
//     - For OpProtocolOperation: rule must have a ProtocolOperationRule, and
//     RPCName must equal req.OperationName or be "*".
//     - For OpNotification: rule must have a NotificationRule, and
//     NotificationName must equal req.OperationName or be "*".
//     - Rules with only a data-node Path are skipped for both op types.
//     c. Check that rule.AccessOperations includes the relevant access type
//     (exec for OpProtocolOperation, read for OpNotification), or is "*".
//     d. If all conditions match, return the rule's Action as a Decision.
//  3. If no rule matched, return DefaultDeny.
//
// Enforce is a pure function with no side effects. It does not modify cfg or
// increment the deny counters on cfg — callers are responsible for state.
func Enforce(cfg Nacm, req Request) Decision {
	if !cfg.EnableNacm {
		return Permit
	}

	for _, rl := range cfg.RuleLists {
		if !groupApplies(rl.Group, req.Groups) {
			continue
		}

		for _, rule := range rl.Rules {
			if !moduleMatches(rule.ModuleName, req.ModuleName) {
				continue
			}

			if !ruleTypeMatches(rule, req) {
				continue
			}

			if !accessOperationMatches(rule.AccessOperations, req.OperationType) {
				continue
			}

			// All conditions matched — return the rule's action.
			if rule.Action == ActionPermit {
				return Permit
			}
			return Deny
		}
	}

	return DefaultDeny
}

// groupApplies reports whether a rule-list's Group list applies to the
// requesting user. An empty ruleGroups slice applies to all users.
// Otherwise, at least one element of ruleGroups must appear in userGroups.
func groupApplies(ruleGroups []string, userGroups []string) bool {
	if len(ruleGroups) == 0 {
		return true
	}
	for _, rg := range ruleGroups {
		for _, ug := range userGroups {
			if rg == ug {
				return true
			}
		}
	}
	return false
}

// moduleMatches reports whether a rule's module-name matches the request's
// module name. A rule module-name of "*" matches any module.
// An empty rule module-name also matches any module (treated as wildcard).
func moduleMatches(ruleModule, reqModule string) bool {
	if ruleModule == "" || ruleModule == "*" {
		return true
	}
	return ruleModule == reqModule
}

// ruleTypeMatches reports whether a rule's type is applicable to the request's
// OperationType, and whether the specific operation name matches.
func ruleTypeMatches(rule Rule, req Request) bool {
	switch req.OperationType {
	case OpProtocolOperation:
		if rule.ProtocolOperation == nil {
			return false
		}
		return nameMatches(rule.ProtocolOperation.RPCName, req.OperationName)

	case OpNotification:
		if rule.Notification == nil {
			return false
		}
		return nameMatches(rule.Notification.NotificationName, req.OperationName)

	default:
		return false
	}
}

// nameMatches reports whether a rule operation name matches the requested
// operation name. A rule name of "*" or "" matches any operation.
func nameMatches(ruleName, reqName string) bool {
	if ruleName == "" || ruleName == "*" {
		return true
	}
	return ruleName == reqName
}

// accessOperationMatches reports whether the rule's access-operations field
// covers the type of access implied by the request's OperationType.
//
// For OpProtocolOperation, the required access type is "exec".
// For OpNotification, the required access type is "read".
//
// The access-operations value is a space-separated list (RFC 8341 §3.2.6).
// A value of "*" matches any access type.
// An empty value is treated as a wildcard (matches all).
func accessOperationMatches(ruleAccess string, opType OperationType) bool {
	if ruleAccess == "" || ruleAccess == "*" {
		return true
	}

	var required string
	switch opType {
	case OpProtocolOperation:
		required = "exec"
	case OpNotification:
		required = "read"
	default:
		return false
	}

	// The access-operations value is space-separated.
	for _, op := range strings.Fields(ruleAccess) {
		if op == "*" || op == required {
			return true
		}
	}
	return false
}
