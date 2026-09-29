package azureinventory

import "github.com/icearp/disco-cli/internal/sdkinv"

// The scope vocabulary list operations enumerate within.
const (
	ScopeResourceGroup   sdkinv.Scope = "resource-group"
	ScopeSubscription    sdkinv.Scope = "subscription"
	ScopeManagementGroup sdkinv.Scope = "management-group"
	ScopeTenant          sdkinv.Scope = "tenant"
	ScopeExtension       sdkinv.Scope = "extension"
)
