package gcpinventory

import "github.com/icearp/disco-cli/internal/sdkinv"

// The scope vocabulary list operations enumerate within.
const (
	ScopeProject        sdkinv.Scope = "project"
	ScopeOrg            sdkinv.Scope = "org"
	ScopeFolder         sdkinv.Scope = "folder"
	ScopeBillingAccount sdkinv.Scope = "billing-account"
	ScopeTenant         sdkinv.Scope = "tenant"
	ScopeGlobal         sdkinv.Scope = "global"
	// ScopeUnscoped: the path opens on a param that takes any container.
	ScopeUnscoped sdkinv.Scope = "unscoped"
)
