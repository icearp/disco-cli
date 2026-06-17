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
		// Cognitive Services accounts surface live secrets on the standard
		// List response (not a dedicated listKeys op): QnAMaker / Metrics
		// Advisor connection strings + search key under apiProperties, plus a
		// credential-bearing migrationToken.
		{Type: TypeCognitiveServicesAccount, Attributes: []redact.Rule{
			{Path: "properties.apiProperties.eventHubConnectionString", Mode: redact.RedactScalar},
			{Path: "properties.apiProperties.qnaAzureSearchEndpointKey", Mode: redact.RedactScalar},
			{Path: "properties.apiProperties.storageAccountConnectionString", Mode: redact.RedactScalar},
			{Path: "properties.migrationToken", Mode: redact.RedactScalar},
		}},
		// NetApp account Active Directory bind password ships on the standard
		// Accounts list response.
		{Type: TypeNetAppAccount, Attributes: []redact.Rule{
			{Path: "properties.activeDirectories[*].password", Mode: redact.RedactScalar},
		}},
		// HDInsight cluster Get/List response echoes the Linux OS profile
		// password (per compute role) and the AD domain-join password.
		// (clusterDefinition.configurations is an untyped map that can also
		// hold gateway/storage secrets; disco's dotted-path redact can't target
		// freeform `any`, so that residual is not covered here.)
		{Type: TypeHDInsightCluster, Attributes: []redact.Rule{
			{Path: "properties.computeProfile.roles[*].osProfile.linuxOperatingSystemProfile.password", Mode: redact.RedactScalar},
			{Path: "properties.securityProfile.domainUserPassword", Mode: redact.RedactScalar},
		}},
		// Stream Analytics job storage account key ships on the streaming-job
		// list response (inputs/outputs are not $expand'd, so their datasource
		// credentials never surface).
		{Type: TypeStreamAnalyticsJob, Attributes: []redact.Rule{
			{Path: "properties.jobStorageAccount.accountKey", Mode: redact.RedactScalar},
		}},
	}
	for _, r := range rules {
		redact.Register(r)
	}
}
