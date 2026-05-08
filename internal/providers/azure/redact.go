package azure

import "codeberg.org/icearp/disco/internal/redact"

// Provider-declared redaction rules. Azure ARM Get responses are mostly
// pointer-style — connection strings, keys, passwords are typically returned
// only via dedicated listKeys / listConnectionStrings ops which disco does
// not call. The rules below cover the few fields that DO surface on standard
// Get responses (administratorLoginPassword on Create-shape echoes for SQL
// flavours) and act as a defensive belt for any future scanner that pulls
// /config sub-resources for App Service.
//
// Key Vault reference URIs (the historical isReferenceURI allowlist) are
// preserved by omission — no rule targets keyVaultSecretId or similar.
func init() {
	rules := []redact.TypeRules{
		// SQL-family administratorLoginPassword (Create-response only, but
		// defensive against any future scanner that surfaces the field).
		{Type: TypeSQLServer, Attributes: []redact.Rule{
			{Path: "properties.administratorLoginPassword", Mode: redact.RedactScalar},
		}},
		{Type: TypePostgreSQLFlexibleServer, Attributes: []redact.Rule{
			{Path: "properties.administratorLoginPassword", Mode: redact.RedactScalar},
		}},
		{Type: TypeMySQLFlexibleServer, Attributes: []redact.Rule{
			{Path: "properties.administratorLoginPassword", Mode: redact.RedactScalar},
		}},
		// App Service / Function App config sub-resource shape. Scanners that
		// fetch /config/appsettings or /config/connectionstrings emit these
		// shapes; rule fires only if those paths are populated. Empty paths
		// no-op (apply walker descends only when the literal segment exists).
		{Type: TypeAppServiceSite, Attributes: []redact.Rule{
			{Path: "properties.siteConfig.appSettings[*].value", Mode: redact.RedactScalar},
			{Path: "properties.siteConfig.connectionStrings[*].connectionString", Mode: redact.RedactScalar},
		}},
		{Type: TypeAppServiceSiteSlot, Attributes: []redact.Rule{
			{Path: "properties.siteConfig.appSettings[*].value", Mode: redact.RedactScalar},
			{Path: "properties.siteConfig.connectionStrings[*].connectionString", Mode: redact.RedactScalar},
		}},
	}
	for _, r := range rules {
		redact.Register(r)
	}
}
