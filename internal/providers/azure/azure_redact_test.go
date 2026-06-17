package azure

import (
	"encoding/json"
	"testing"

	"codeberg.org/icearp/disco/internal/redact"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/avs/armavs"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/desktopvirtualization/armdesktopvirtualization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hdinsight/armhdinsight"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/horizondb/armhorizondb"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iothub/armiothub"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mongocluster/armmongocluster"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/netapp/armnetapp/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresqlhsc/armpostgresqlhsc"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/streamanalytics/armstreamanalytics"
)

func applyAndDecode(t *testing.T, resourceType string, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := redact.Apply(resourceType, string(raw))
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal redacted: %v", err)
	}
	return got
}

func TestRedact_SQLServer_AdminPassword(t *testing.T) {
	in := map[string]any{
		"properties": map[string]any{
			"administratorLogin":         "sqladmin",
			"administratorLoginPassword": "hunter2",
		},
	}
	got := applyAndDecode(t, TypeSQLServer, in)
	props := got["properties"].(map[string]any)
	if props["administratorLoginPassword"] != redact.Placeholder {
		t.Errorf("password not redacted: %v", props["administratorLoginPassword"])
	}
	if props["administratorLogin"] != "sqladmin" {
		t.Errorf("login clobbered")
	}
}

func TestRedact_AppServiceSite_AppSettingsAndConnStrings(t *testing.T) {
	in := map[string]any{
		"properties": map[string]any{
			"siteConfig": map[string]any{
				"appSettings": []any{
					map[string]any{"name": "DB_PASS", "value": "hunter2"},
					map[string]any{"name": "LOG_LEVEL", "value": "debug"},
				},
				"connectionStrings": []any{
					map[string]any{"name": "default", "type": "SQLAzure", "connectionString": "Server=...;Password=hunter2"},
				},
			},
		},
	}
	got := applyAndDecode(t, TypeAppServiceSite, in)
	cfg := got["properties"].(map[string]any)["siteConfig"].(map[string]any)
	for _, e := range cfg["appSettings"].([]any) {
		em := e.(map[string]any)
		if em["value"] != redact.Placeholder {
			t.Errorf("appSetting value not redacted: %v", em)
		}
		if em["name"] == "" {
			t.Errorf("appSetting name clobbered")
		}
	}
	cs := cfg["connectionStrings"].([]any)[0].(map[string]any)
	if cs["connectionString"] != redact.Placeholder {
		t.Errorf("connectionString not redacted: %v", cs["connectionString"])
	}
	if cs["type"] != "SQLAzure" {
		t.Errorf("type clobbered")
	}
}

// TestRedact_CognitiveServicesAccount asserts the plaintext secrets that ship
// on the standard Accounts.List response (apiProperties connection strings /
// search key + migrationToken) come back redacted, while non-secret fields
// (endpoint) survive. Built from the real SDK struct so an SDK field rename
// breaks the test on go mod tidy rather than silently leaking.
func TestRedact_CognitiveServicesAccount(t *testing.T) {
	acct := armcognitiveservices.Account{
		Properties: &armcognitiveservices.AccountProperties{
			Endpoint:       to.Ptr("https://ai.cognitiveservices.azure.com/"),
			MigrationToken: to.Ptr("secret-migration-token"),
			APIProperties: &armcognitiveservices.APIProperties{
				EventHubConnectionString:       to.Ptr("Endpoint=sb://eh/;SharedAccessKey=secret"),
				QnaAzureSearchEndpointKey:      to.Ptr("search-admin-key"),
				StorageAccountConnectionString: to.Ptr("DefaultEndpointsProtocol=https;AccountKey=secret"),
			},
		},
	}
	got := applyAndDecode(t, TypeCognitiveServicesAccount, acct)
	props := got["properties"].(map[string]any)
	if props["migrationToken"] != redact.Placeholder {
		t.Errorf("migrationToken not redacted: %v", props["migrationToken"])
	}
	if props["endpoint"] != "https://ai.cognitiveservices.azure.com/" {
		t.Errorf("endpoint clobbered: %v", props["endpoint"])
	}
	api := props["apiProperties"].(map[string]any)
	for _, k := range []string{"eventHubConnectionString", "qnaAzureSearchEndpointKey", "storageAccountConnectionString"} {
		if api[k] != redact.Placeholder {
			t.Errorf("apiProperties.%s not redacted: %v", k, api[k])
		}
	}
}

// TestRedact_NetAppAccount asserts the Active Directory bind password shipped
// on the NetApp Accounts list response is redacted while a sibling field
// (smbServerName) survives.
func TestRedact_NetAppAccount(t *testing.T) {
	acct := armnetapp.Account{
		Properties: &armnetapp.AccountProperties{
			ActiveDirectories: []*armnetapp.ActiveDirectory{
				{Password: to.Ptr("ad-bind-secret"), SmbServerName: to.Ptr("SMBSRV")},
			},
		},
	}
	got := applyAndDecode(t, TypeNetAppAccount, acct)
	ad := got["properties"].(map[string]any)["activeDirectories"].([]any)[0].(map[string]any)
	if ad["password"] != redact.Placeholder {
		t.Errorf("AD password not redacted: %v", ad["password"])
	}
	if ad["smbServerName"] != "SMBSRV" {
		t.Errorf("smbServerName clobbered: %v", ad["smbServerName"])
	}
}

// TestRedact_HDInsightCluster asserts the Linux OS profile password (per role)
// and the AD domain-join password are redacted, while a sibling (username)
// survives.
func TestRedact_HDInsightCluster(t *testing.T) {
	cluster := armhdinsight.Cluster{
		Properties: &armhdinsight.ClusterGetProperties{
			ComputeProfile: &armhdinsight.ComputeProfile{
				Roles: []*armhdinsight.Role{
					{OSProfile: &armhdinsight.OsProfile{
						LinuxOperatingSystemProfile: &armhdinsight.LinuxOperatingSystemProfile{
							Username: to.Ptr("sshuser"),
							Password: to.Ptr("node-secret"),
						},
					}},
				},
			},
			SecurityProfile: &armhdinsight.SecurityProfile{
				DomainUsername:     to.Ptr("dom\\admin"),
				DomainUserPassword: to.Ptr("domain-secret"),
			},
		},
	}
	got := applyAndDecode(t, TypeHDInsightCluster, cluster)
	props := got["properties"].(map[string]any)
	role := props["computeProfile"].(map[string]any)["roles"].([]any)[0].(map[string]any)
	los := role["osProfile"].(map[string]any)["linuxOperatingSystemProfile"].(map[string]any)
	if los["password"] != redact.Placeholder {
		t.Errorf("linux password not redacted: %v", los["password"])
	}
	if los["username"] != "sshuser" {
		t.Errorf("username clobbered: %v", los["username"])
	}
	sec := props["securityProfile"].(map[string]any)
	if sec["domainUserPassword"] != redact.Placeholder {
		t.Errorf("domainUserPassword not redacted: %v", sec["domainUserPassword"])
	}
	if sec["domainUsername"] != "dom\\admin" {
		t.Errorf("domainUsername clobbered: %v", sec["domainUsername"])
	}
}

// TestRedact_StreamAnalyticsJob asserts the job storage account key shipped on
// the streaming-job list response is redacted while the account name survives.
func TestRedact_StreamAnalyticsJob(t *testing.T) {
	job := armstreamanalytics.StreamingJob{
		Properties: &armstreamanalytics.StreamingJobProperties{
			JobStorageAccount: &armstreamanalytics.JobStorageAccount{
				AccountName: to.Ptr("sajobsa"),
				AccountKey:  to.Ptr("storage-key-secret"),
			},
		},
	}
	got := applyAndDecode(t, TypeStreamAnalyticsJob, job)
	jsa := got["properties"].(map[string]any)["jobStorageAccount"].(map[string]any)
	if jsa["accountKey"] != redact.Placeholder {
		t.Errorf("accountKey not redacted: %v", jsa["accountKey"])
	}
	if jsa["accountName"] != "sajobsa" {
		t.Errorf("accountName clobbered: %v", jsa["accountName"])
	}
}

// TestRedact_IoTHub asserts the SAS policy primary key and a routing-endpoint
// connection string are redacted while a sibling (endpoint name) survives.
func TestRedact_IoTHub(t *testing.T) {
	hub := armiothub.Description{
		Properties: &armiothub.Properties{
			AuthorizationPolicies: []*armiothub.SharedAccessSignatureAuthorizationRule{
				{KeyName: to.Ptr("iothubowner"), PrimaryKey: to.Ptr("primary-secret"), SecondaryKey: to.Ptr("secondary-secret")},
			},
			Routing: &armiothub.RoutingProperties{
				Endpoints: &armiothub.RoutingEndpoints{
					EventHubs: []*armiothub.RoutingEventHubProperties{
						{Name: to.Ptr("eh-out"), ConnectionString: to.Ptr("Endpoint=sb://eh/;SharedAccessKey=secret")},
					},
				},
			},
		},
	}
	got := applyAndDecode(t, TypeIoTHub, hub)
	props := got["properties"].(map[string]any)
	pol := props["authorizationPolicies"].([]any)[0].(map[string]any)
	if pol["primaryKey"] != redact.Placeholder || pol["secondaryKey"] != redact.Placeholder {
		t.Errorf("SAS keys not redacted: %v", pol)
	}
	eh := props["routing"].(map[string]any)["endpoints"].(map[string]any)["eventHubs"].([]any)[0].(map[string]any)
	if eh["connectionString"] != redact.Placeholder {
		t.Errorf("routing connectionString not redacted: %v", eh["connectionString"])
	}
	if eh["name"] != "eh-out" {
		t.Errorf("endpoint name clobbered: %v", eh["name"])
	}
}

// TestRedact_AVSPrivateCloud asserts the NSX-T and vCenter admin passwords are
// redacted.
func TestRedact_AVSPrivateCloud(t *testing.T) {
	pc := armavs.PrivateCloud{
		Properties: &armavs.PrivateCloudProperties{
			NsxtPassword:    to.Ptr("nsxt-secret"),
			VcenterPassword: to.Ptr("vcenter-secret"),
			IdentitySources: []*armavs.IdentitySource{
				{Name: to.Ptr("ad"), Password: to.Ptr("ldap-bind-secret")},
			},
		},
	}
	got := applyAndDecode(t, TypeAVSPrivateCloud, pc)
	props := got["properties"].(map[string]any)
	if props["nsxtPassword"] != redact.Placeholder || props["vcenterPassword"] != redact.Placeholder {
		t.Errorf("AVS passwords not redacted: %v", props)
	}
	idsrc := props["identitySources"].([]any)[0].(map[string]any)
	if idsrc["password"] != redact.Placeholder {
		t.Errorf("identity-source bind password not redacted: %v", idsrc["password"])
	}
	if idsrc["name"] != "ad" {
		t.Errorf("identity-source name clobbered: %v", idsrc["name"])
	}
}

// TestRedact_DVCHostPool asserts the host-pool registration token is redacted.
func TestRedact_DVCHostPool(t *testing.T) {
	hp := armdesktopvirtualization.HostPool{
		Properties: &armdesktopvirtualization.HostPoolProperties{
			RegistrationInfo: &armdesktopvirtualization.RegistrationInfo{Token: to.Ptr("join-token-secret")},
		},
	}
	got := applyAndDecode(t, TypeDVCHostPool, hp)
	ri := got["properties"].(map[string]any)["registrationInfo"].(map[string]any)
	if ri["token"] != redact.Placeholder {
		t.Errorf("registration token not redacted: %v", ri["token"])
	}
}

// TestRedact_DashboardGrafana asserts the SMTP password is redacted while a
// sibling (host) survives.
func TestRedact_DashboardGrafana(t *testing.T) {
	g := armdashboard.ManagedGrafana{
		Properties: &armdashboard.ManagedGrafanaProperties{
			GrafanaConfigurations: &armdashboard.GrafanaConfigurations{
				SMTP: &armdashboard.SMTP{Host: to.Ptr("smtp:587"), Password: to.Ptr("smtp-secret")},
			},
		},
	}
	got := applyAndDecode(t, TypeDashboardGrafana, g)
	smtp := got["properties"].(map[string]any)["grafanaConfigurations"].(map[string]any)["smtp"].(map[string]any)
	if smtp["password"] != redact.Placeholder {
		t.Errorf("SMTP password not redacted: %v", smtp["password"])
	}
	if smtp["host"] != "smtp:587" {
		t.Errorf("SMTP host clobbered: %v", smtp["host"])
	}
}

// TestRedact_BotServiceBot asserts the LUIS key, App Insights API key,
// publishing credentials, and migration token on the bot list response are
// redacted while a sibling (msaAppId) survives.
func TestRedact_BotServiceBot(t *testing.T) {
	bot := armbotservice.Bot{
		Properties: &armbotservice.BotProperties{
			MsaAppID:                   to.Ptr("00000000-0000-0000-0000-000000000000"),
			LuisKey:                    to.Ptr("luis-secret"),
			DeveloperAppInsightsAPIKey: to.Ptr("appinsights-secret"),
			PublishingCredentials:      to.Ptr("publish-secret"),
			MigrationToken:             to.Ptr("migration-secret"),
		},
	}
	got := applyAndDecode(t, TypeBotServiceBot, bot)
	props := got["properties"].(map[string]any)
	for _, k := range []string{"luisKey", "developerAppInsightsApiKey", "publishingCredentials", "migrationToken"} {
		if props[k] != redact.Placeholder {
			t.Errorf("properties.%s not redacted: %v", k, props[k])
		}
	}
	if props["msaAppId"] != "00000000-0000-0000-0000-000000000000" {
		t.Errorf("msaAppId clobbered: %v", props["msaAppId"])
	}
}

func TestRedact_AppServiceSite_KeyVaultRefPreserved(t *testing.T) {
	// Azure Key Vault reference URIs sit at properties.siteConfig.appSettings
	// values; previous sanitize.go preserved them via isReferenceURI shape
	// recogniser. Under per-type rules, the value is a plain scalar at the
	// rule path — REDACTED. That's the intentional trade-off: KV-ref consumers
	// (resolvers wiring App Service → KV) read the *resolved* settings via
	// Microsoft.Web/sites/config Get with KeyVault references, not the raw
	// settings array. If a resolver depends on this URI, it should read from
	// the dedicated config resource (a separate scanner emit).
	in := map[string]any{
		"properties": map[string]any{
			"siteConfig": map[string]any{
				"appSettings": []any{
					map[string]any{"name": "KV_REF", "value": "@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/foo/abc)"},
				},
			},
		},
	}
	got := applyAndDecode(t, TypeAppServiceSite, in)
	v := got["properties"].(map[string]any)["siteConfig"].(map[string]any)["appSettings"].([]any)[0].(map[string]any)["value"]
	if v != redact.Placeholder {
		t.Errorf("expected redaction; got %v", v)
	}
}

func TestRedact_HorizonDBCluster_AdminPassword(t *testing.T) {
	c := armhorizondb.Cluster{Properties: &armhorizondb.ClusterProperties{
		AdministratorLogin:         to.Ptr("pgadmin"),
		AdministratorLoginPassword: to.Ptr("hunter2"),
	}}
	got := applyAndDecode(t, TypeHorizonDBCluster, c)
	props := got["properties"].(map[string]any)
	if props["administratorLoginPassword"] != redact.Placeholder {
		t.Errorf("password not redacted: %v", props["administratorLoginPassword"])
	}
	if props["administratorLogin"] != "pgadmin" {
		t.Errorf("login clobbered")
	}
}

func TestRedact_PostgreSQLServerGroupV2_AdminPassword(t *testing.T) {
	c := armpostgresqlhsc.Cluster{Properties: &armpostgresqlhsc.ClusterProperties{
		AdministratorLoginPassword: to.Ptr("hunter2"),
	}}
	got := applyAndDecode(t, TypePostgreSQLServerGroupV2, c)
	props := got["properties"].(map[string]any)
	if props["administratorLoginPassword"] != redact.Placeholder {
		t.Errorf("password not redacted: %v", props["administratorLoginPassword"])
	}
}

func TestRedact_MongoCluster_ConnectionString(t *testing.T) {
	c := armmongocluster.MongoCluster{Properties: &armmongocluster.Properties{
		ConnectionString: to.Ptr("mongodb+srv://u:p@h.mongocluster.cosmos.azure.com/"),
		Administrator: &armmongocluster.AdministratorProperties{
			UserName: to.Ptr("mongoadmin"),
			Password: to.Ptr("hunter2"),
		},
	}}
	got := applyAndDecode(t, TypeMongoCluster, c)
	props := got["properties"].(map[string]any)
	if props["connectionString"] != redact.Placeholder {
		t.Errorf("connectionString not redacted: %v", props["connectionString"])
	}
	admin := props["administrator"].(map[string]any)
	if admin["password"] != redact.Placeholder {
		t.Errorf("administrator password not redacted: %v", admin["password"])
	}
	if admin["userName"] != "mongoadmin" {
		t.Errorf("userName clobbered: %v", admin["userName"])
	}
}
