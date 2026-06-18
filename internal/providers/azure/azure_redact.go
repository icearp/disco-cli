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
		// IoT Hub routing endpoints + built-in SAS policies expose connection
		// strings / keys on the standard list response.
		{Type: TypeIoTHub, Attributes: []redact.Rule{
			{Path: "properties.authorizationPolicies[*].primaryKey", Mode: redact.RedactScalar},
			{Path: "properties.authorizationPolicies[*].secondaryKey", Mode: redact.RedactScalar},
			{Path: "properties.routing.endpoints.eventHubs[*].connectionString", Mode: redact.RedactScalar},
			{Path: "properties.routing.endpoints.serviceBusQueues[*].connectionString", Mode: redact.RedactScalar},
			{Path: "properties.routing.endpoints.serviceBusTopics[*].connectionString", Mode: redact.RedactScalar},
			{Path: "properties.routing.endpoints.storageContainers[*].connectionString", Mode: redact.RedactScalar},
			{Path: "properties.routing.endpoints.cosmosDBSqlContainers[*].primaryKey", Mode: redact.RedactScalar},
			{Path: "properties.storageEndpoints.*.connectionString", Mode: redact.RedactScalar},
		}},
		// AVD host pool registration token is a join credential on the list response.
		{Type: TypeDVCHostPool, Attributes: []redact.Rule{
			{Path: "properties.registrationInfo.token", Mode: redact.RedactScalar},
		}},
		// AVS private cloud echoes NSX-T / vCenter admin passwords and AD
		// identity-source bind passwords.
		{Type: TypeAVSPrivateCloud, Attributes: []redact.Rule{
			{Path: "properties.nsxtPassword", Mode: redact.RedactScalar},
			{Path: "properties.vcenterPassword", Mode: redact.RedactScalar},
			{Path: "properties.identitySources[*].password", Mode: redact.RedactScalar},
		}},
		// Managed Grafana SMTP password ships under grafanaConfigurations.
		{Type: TypeDashboardGrafana, Attributes: []redact.Rule{
			{Path: "properties.grafanaConfigurations.smtp.password", Mode: redact.RedactScalar},
		}},
		// Bot Service bots carry live LUIS / App Insights keys, publishing
		// credentials, and a migration token on the bot list response. (The
		// channel / connection-setting secrets live on separate child clients
		// disco does not call; appPasswordHint / cmekKeyVaultUrl are KV
		// reference URIs, preserved by omission.)
		{Type: TypeBotServiceBot, Attributes: []redact.Rule{
			{Path: "properties.luisKey", Mode: redact.RedactScalar},
			{Path: "properties.developerAppInsightsApiKey", Mode: redact.RedactScalar},
			{Path: "properties.publishingCredentials", Mode: redact.RedactScalar},
			{Path: "properties.migrationToken", Mode: redact.RedactScalar},
		}},
		// Wave 2 database flavours that echo a write-shape admin password /
		// connection string on the standard list response.
		{Type: TypeHorizonDBCluster, Attributes: []redact.Rule{
			{Path: "properties.administratorLoginPassword", Mode: redact.RedactScalar},
		}},
		{Type: TypePostgreSQLServerGroupV2, Attributes: []redact.Rule{
			{Path: "properties.administratorLoginPassword", Mode: redact.RedactScalar},
		}},
		{Type: TypeMongoCluster, Attributes: []redact.Rule{
			{Path: "properties.connectionString", Mode: redact.RedactScalar},
			{Path: "properties.administrator.password", Mode: redact.RedactScalar},
		}},
		// Wave 3 Arc services that echo connect-credentials on the list response.
		{Type: TypeAzureArcDataController, Attributes: []redact.Rule{
			{Path: "properties.logsDashboardCredential.password", Mode: redact.RedactScalar},
			{Path: "properties.metricsDashboardCredential.password", Mode: redact.RedactScalar},
			// Log Analytics workspace shared key echoed on the list response.
			{Path: "properties.logAnalyticsWorkspaceConfig.primaryKey", Mode: redact.RedactScalar},
		}},
		// Arc data Postgres / SQL managed instances echo the basic-login admin
		// password on their list response.
		{Type: TypeAzureArcDataPostgres, Attributes: []redact.Rule{
			{Path: "properties.basicLoginInformation.password", Mode: redact.RedactScalar},
		}},
		{Type: TypeAzureArcDataSQLManagedInstance, Attributes: []redact.Rule{
			{Path: "properties.basicLoginInformation.password", Mode: redact.RedactScalar},
		}},
		{Type: TypeConnectedVMwareVCenter, Attributes: []redact.Rule{
			{Path: "properties.credentials.password", Mode: redact.RedactScalar},
		}},
		{Type: TypeScVmmServer, Attributes: []redact.Rule{
			{Path: "properties.credentials.password", Mode: redact.RedactScalar},
		}},
		// Custom Location optional authentication carries a kubeconfig value
		// (bearer token / client cert) — redact it.
		{Type: TypeCustomLocation, Attributes: []redact.Rule{
			{Path: "properties.authentication.value", Mode: redact.RedactScalar},
		}},
		// Wave 4: Managed Network Fabric echoes the terminal-server connection
		// password on the list response.
		{Type: TypeManagedNetworkFabric, Attributes: []redact.Rule{
			{Path: "properties.terminalServerConfiguration.password", Mode: redact.RedactScalar},
		}},
		// Operator Nexus clusters carry baseboard-management-controller and
		// storage-appliance admin passwords in each rack definition (the SDK
		// permits a plaintext password until the cluster moves to managed
		// identity). Redact across the single aggregator rack and every compute
		// rack.
		{Type: TypeNetworkCloudCluster, Attributes: []redact.Rule{
			{Path: "properties.aggregatorOrSingleRackDefinition.bareMetalMachineConfigurationData[*].bmcCredentials.password", Mode: redact.RedactScalar},
			{Path: "properties.aggregatorOrSingleRackDefinition.storageApplianceConfigurationData[*].adminCredentials.password", Mode: redact.RedactScalar},
			{Path: "properties.computeRackDefinitions[*].bareMetalMachineConfigurationData[*].bmcCredentials.password", Mode: redact.RedactScalar},
			{Path: "properties.computeRackDefinitions[*].storageApplianceConfigurationData[*].adminCredentials.password", Mode: redact.RedactScalar},
		}},
		// Wave 5: SQL Server on Azure VMs echo a full set of write-shape
		// credentials on the list response — auto-backup encryption password +
		// storage key, WSFC domain account passwords, Key Vault service-
		// principal secret, and the sysadmin auth-update password.
		{Type: TypeSQLVirtualMachine, Attributes: []redact.Rule{
			{Path: "properties.autoBackupSettings.password", Mode: redact.RedactScalar},
			{Path: "properties.autoBackupSettings.storageAccessKey", Mode: redact.RedactScalar},
			{Path: "properties.keyVaultCredentialSettings.servicePrincipalSecret", Mode: redact.RedactScalar},
			{Path: "properties.wsfcDomainCredentials.clusterBootstrapAccountPassword", Mode: redact.RedactScalar},
			{Path: "properties.wsfcDomainCredentials.clusterOperatorAccountPassword", Mode: redact.RedactScalar},
			{Path: "properties.wsfcDomainCredentials.sqlServiceAccountPassword", Mode: redact.RedactScalar},
			{Path: "properties.serverConfigurationsManagementSettings.sqlConnectivityUpdateSettings.sqlAuthUpdatePassword", Mode: redact.RedactScalar},
		}},
		// HANA-on-Azure SAP monitor echoes the Log Analytics workspace shared key.
		{Type: TypeHanaOnAzureSapMonitor, Attributes: []redact.Rule{
			{Path: "properties.logAnalyticsWorkspaceSharedKey", Mode: redact.RedactScalar},
		}},
		// Compute Fleet embeds a VMSS base profile; its OS profile can carry a
		// plaintext admin password and base64 customData (cloud-init, which can
		// hold app secrets).
		{Type: TypeComputeFleet, Attributes: []redact.Rule{
			{Path: "properties.computeProfile.baseVirtualMachineProfile.osProfile.adminPassword", Mode: redact.RedactScalar},
			{Path: "properties.computeProfile.baseVirtualMachineProfile.osProfile.customData", Mode: redact.RedactScalar},
		}},
		// Wave 6: Edge Order items echo a read-only SAS key for the reverse-
		// shipment label on the list response.
		{Type: TypeEdgeOrderItem, Attributes: []redact.Rule{
			{Path: "properties.orderItemDetails.reverseShippingDetails.sasKeyForLabel", Mode: redact.RedactScalar},
		}},
		// Wave 8 write-shape credentials echoed on list responses.
		// (Public certificate bodies — certificateOrders signedCertificate/csr,
		// confidentialLedger certBasedSecurityPrincipals[].cert — and public
		// OIDC client/tenant IDs are preserved by omission, not redacted.)
		{Type: TypeDomain, Attributes: []redact.Rule{
			// Domain transfer / EPP authorization code.
			{Path: "properties.authCode", Mode: redact.RedactScalar},
		}},
		{Type: TypeOpenShiftCluster, Attributes: []redact.Rule{
			{Path: "properties.servicePrincipalProfile.clientSecret", Mode: redact.RedactScalar},
			{Path: "properties.clusterProfile.pullSecret", Mode: redact.RedactScalar},
		}},
		{Type: TypeLabServicesLab, Attributes: []redact.Rule{
			{Path: "properties.virtualMachineProfile.adminUser.password", Mode: redact.RedactScalar},
			{Path: "properties.virtualMachineProfile.nonAdminUser.password", Mode: redact.RedactScalar},
		}},
	}
	for _, r := range rules {
		redact.Register(r)
	}
}
