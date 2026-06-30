module codeberg.org/icearp/disco

go 1.25.8

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.22.0
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.14.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/analysisservices/armanalysisservices v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apicenter/armapicenter v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3 v3.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appplatform/armappplatform v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/attestation/armattestation v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2 v2.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/automanage/armautomanage v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/automation/armautomation v0.10.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/avs/armavs v1.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurearcdata/armazurearcdata v0.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurestackhci/armazurestackhci v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurestackhci/armazurestackhcivm v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/baremetalinfrastructure/armbaremetalinfrastructure v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/batch/armbatch v1.2.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/blueprint/armblueprint v0.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/certificateregistration/armcertificateregistration v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/chaos/armchaos v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cloudhealth/armcloudhealth v0.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices v1.8.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/communication/armcommunication/v2 v2.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6 v6.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/confidentialledger/armconfidentialledger v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/connectedcache/armconnectedcache v0.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/connectedvmware/armconnectedvmware v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6 v6.6.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservicefleet/armcontainerservicefleet/v3 v3.0.0-beta.5
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/customproviders/armcustomproviders v0.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databasewatcher/armdatabasewatcher v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databox/armdatabox v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databoxedge/armdataboxedge v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datafactory/armdatafactory v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datamigration/armdatamigration v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dataprotection/armdataprotection v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datashare/armdatashare v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dependencymap/armdependencymap v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/desktopvirtualization/armdesktopvirtualization/v2 v2.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devcenter/armdevcenter v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devhub/armdevhub v0.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/deviceprovisioningservices/armdeviceprovisioningservices v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/deviceregistry/armdeviceregistry v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/deviceupdate/armdeviceupdate v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devopsinfrastructure/armdevopsinfrastructure v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devtestlabs/armdevtestlabs v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/digitaltwins/armdigitaltwins v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dnsresolver/armdnsresolver v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/domainregistration/armdomainregistration v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/durabletask/armdurabletask v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/edgemarketplace/armedgemarketplace v0.0.0-20260627092125-488a35f3021f
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/edgeorder/armedgeorder v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/edgezones/armedgezones v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/elastic/armelastic v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/elasticsan/armelasticsan v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2 v2.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/extendedlocation/armextendedlocation v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/fabric/armfabric v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/fileshares/armfileshares v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/fluidrelay/armfluidrelay v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/frontdoor/armfrontdoor v1.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/graphservices/armgraphservices v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hardwaresecuritymodules/armhardwaresecuritymodules v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hdinsight/armhdinsight v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/healthbot/armhealthbot v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/healthcareapis/armhealthcareapis v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/healthdataaiservices/armhealthdataaiservices v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/horizondb/armhorizondb v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridcompute/armhybridcompute v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridconnectivity/armhybridconnectivity v1.2.0-beta.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridcontainerservice/armhybridcontainerservice v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridkubernetes/armhybridkubernetes v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridnetwork/armhybridnetwork v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/integrationspaces/armintegrationspaces v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iotcentral/armiotcentral v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iotfirmwaredefense/armiotfirmwaredefense v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iothub/armiothub v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iotoperations/armiotoperations v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault v1.5.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/kusto/armkusto v1.3.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/labservices/armlabservices v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/largeinstance/armlargeinstance v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/loadtesting/armloadtesting v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/logic/armlogic v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v4 v4.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/maintenance/armmaintenance v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managednetworkfabric/armmanagednetworkfabric v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managedservices/armmanagedservices v0.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/maps/armmaps v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/migrationassessment/armmigrationassessment v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mongocluster/armmongocluster v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor v0.11.0 // PINNED: do not bump to v0.12.0 — its TypeSpec regen dropped the modern DiagnosticSettings (2021-05-01-preview) API monitor_resolvers.go needs. See azure/CLAUDE.md.
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/netapp/armnetapp/v7 v7.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6 v6.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/networkcloud/armnetworkcloud v1.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/networkfunction/armnetworkfunction v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/onlineexperimentation/armonlineexperimentation v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationsmanagement/armoperationsmanagement v0.8.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/peering/armpeering v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/planetarycomputer/armplanetarycomputer v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/playwrighttesting/armplaywrighttesting v1.0.2
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/policyinsights/armpolicyinsights v0.10.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresqlhsc/armpostgresqlhsc v0.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/powerbidedicated/armpowerbidedicated v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/powerplatform/armpowerplatform v0.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/purview/armpurview v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/quantum/armquantum v0.8.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/quota/armquota v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/recoveryservices/armrecoveryservices v1.6.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/recoveryservicesdatareplication/armrecoveryservicesdatareplication v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redhatopenshift/armredhatopenshift v1.6.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redisenterprise/armredisenterprise v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/relay/armrelay v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resiliencemanagement/armresiliencemanagement v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourceconnector/armresourceconnector v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/saas/armsaas v0.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/scvmm/armscvmm v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/search/armsearch v1.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/security/armsecurity v0.15.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicefabric/armservicefabric v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicefabricmanagedclusters/armservicefabricmanagedclusters v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicenetworking/armservicenetworking v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/signalr/armsignalr v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/solutions/armmanagedapplications v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sphere/armsphere v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/springappdiscovery/armspringappdiscovery v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sqlvirtualmachine/armsqlvirtualmachine v0.11.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/standbypool/armstandbypool v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage v1.8.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storageactions/armstorageactions v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagecache/armstoragecache v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagediscovery/armstoragediscovery v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagemover/armstoragemover v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagesync/armstoragesync v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/streamanalytics/armstreamanalytics v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/synapse/armsynapse v0.8.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trafficmanager/armtrafficmanager v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trustedsigning/armtrustedsigning v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/virtualmachineimagebuilder/armvirtualmachineimagebuilder v1.2.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/webpubsub/armwebpubsub v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/workloads/armworkloads v1.1.0
	github.com/Masterminds/squirrel v1.5.4
	github.com/adrg/xdg v0.5.3
	github.com/aws/aws-sdk-go-v2 v1.42.0
	github.com/aws/aws-sdk-go-v2/config v1.32.25
	github.com/aws/aws-sdk-go-v2/credentials v1.19.24
	github.com/aws/aws-sdk-go-v2/feature/rds/auth v1.6.29
	github.com/aws/aws-sdk-go-v2/service/accessanalyzer v1.49.5
	github.com/aws/aws-sdk-go-v2/service/acm v1.40.0
	github.com/aws/aws-sdk-go-v2/service/acmpca v1.47.6
	github.com/aws/aws-sdk-go-v2/service/aiops v1.7.7
	github.com/aws/aws-sdk-go-v2/service/amp v1.44.0
	github.com/aws/aws-sdk-go-v2/service/amplify v1.39.4
	github.com/aws/aws-sdk-go-v2/service/amplifyuibuilder v1.29.6
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.40.6
	github.com/aws/aws-sdk-go-v2/service/apigatewayv2 v1.35.6
	github.com/aws/aws-sdk-go-v2/service/appconfig v1.44.6
	github.com/aws/aws-sdk-go-v2/service/appflow v1.52.1
	github.com/aws/aws-sdk-go-v2/service/appintegrations v1.38.2
	github.com/aws/aws-sdk-go-v2/service/applicationautoscaling v1.43.0
	github.com/aws/aws-sdk-go-v2/service/applicationinsights v1.35.6
	github.com/aws/aws-sdk-go-v2/service/applicationsignals v1.23.0
	github.com/aws/aws-sdk-go-v2/service/appmesh v1.36.4
	github.com/aws/aws-sdk-go-v2/service/apprunner v1.40.6
	github.com/aws/aws-sdk-go-v2/service/appstream v1.61.0
	github.com/aws/aws-sdk-go-v2/service/appsync v1.54.4
	github.com/aws/aws-sdk-go-v2/service/arcregionswitch v1.9.3
	github.com/aws/aws-sdk-go-v2/service/arczonalshift v1.23.5
	github.com/aws/aws-sdk-go-v2/service/athena v1.58.4
	github.com/aws/aws-sdk-go-v2/service/auditmanager v1.47.1
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.67.4
	github.com/aws/aws-sdk-go-v2/service/autoscalingplans v1.31.5
	github.com/aws/aws-sdk-go-v2/service/b2bi v1.0.0-preview.110
	github.com/aws/aws-sdk-go-v2/service/backup v1.57.6
	github.com/aws/aws-sdk-go-v2/service/backupgateway v1.27.5
	github.com/aws/aws-sdk-go-v2/service/batch v1.66.0
	github.com/aws/aws-sdk-go-v2/service/bcmdataexports v1.16.7
	github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator v1.11.7
	github.com/aws/aws-sdk-go-v2/service/bedrock v1.64.0
	github.com/aws/aws-sdk-go-v2/service/bedrockagent v1.56.0
	github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol v1.45.0
	github.com/aws/aws-sdk-go-v2/service/billing v1.11.7
	github.com/aws/aws-sdk-go-v2/service/billingconductor v1.30.5
	github.com/aws/aws-sdk-go-v2/service/braket v1.41.6
	github.com/aws/aws-sdk-go-v2/service/budgets v1.44.6
	github.com/aws/aws-sdk-go-v2/service/chatbot v1.15.6
	github.com/aws/aws-sdk-go-v2/service/chimesdkidentity v1.28.7
	github.com/aws/aws-sdk-go-v2/service/cleanrooms v1.45.6
	github.com/aws/aws-sdk-go-v2/service/cleanroomsml v1.24.7
	github.com/aws/aws-sdk-go-v2/service/cloud9 v1.34.6
	github.com/aws/aws-sdk-go-v2/service/cloudformation v1.72.1
	github.com/aws/aws-sdk-go-v2/service/cloudfront v1.65.2
	github.com/aws/aws-sdk-go-v2/service/cloudtrail v1.56.4
	github.com/aws/aws-sdk-go-v2/service/cloudwatch v1.59.0
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.78.0
	github.com/aws/aws-sdk-go-v2/service/codeartifact v1.39.7
	github.com/aws/aws-sdk-go-v2/service/codebuild v1.69.4
	github.com/aws/aws-sdk-go-v2/service/codecommit v1.34.4
	github.com/aws/aws-sdk-go-v2/service/codedeploy v1.36.4
	github.com/aws/aws-sdk-go-v2/service/codeguruprofiler v1.30.6
	github.com/aws/aws-sdk-go-v2/service/codegurureviewer v1.35.5
	github.com/aws/aws-sdk-go-v2/service/codepipeline v1.47.4
	github.com/aws/aws-sdk-go-v2/service/codestarconnections v1.36.5
	github.com/aws/aws-sdk-go-v2/service/codestarnotifications v1.32.6
	github.com/aws/aws-sdk-go-v2/service/cognitoidentity v1.34.4
	github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider v1.62.0
	github.com/aws/aws-sdk-go-v2/service/comprehend v1.41.6
	github.com/aws/aws-sdk-go-v2/service/configservice v1.64.1
	github.com/aws/aws-sdk-go-v2/service/connect v1.177.0
	github.com/aws/aws-sdk-go-v2/service/connectcampaigns v1.21.4
	github.com/aws/aws-sdk-go-v2/service/connectcampaignsv2 v1.14.6
	github.com/aws/aws-sdk-go-v2/service/connectcases v1.42.6
	github.com/aws/aws-sdk-go-v2/service/controltower v1.29.6
	github.com/aws/aws-sdk-go-v2/service/costandusagereportservice v1.35.5
	github.com/aws/aws-sdk-go-v2/service/costexplorer v1.65.1
	github.com/aws/aws-sdk-go-v2/service/customerprofiles v1.62.5
	github.com/aws/aws-sdk-go-v2/service/databasemigrationservice v1.64.4
	github.com/aws/aws-sdk-go-v2/service/databrew v1.40.8
	github.com/aws/aws-sdk-go-v2/service/datasync v1.59.7
	github.com/aws/aws-sdk-go-v2/service/datazone v1.63.0
	github.com/aws/aws-sdk-go-v2/service/dax v1.30.2
	github.com/aws/aws-sdk-go-v2/service/deadline v1.33.1
	github.com/aws/aws-sdk-go-v2/service/detective v1.39.5
	github.com/aws/aws-sdk-go-v2/service/devopsagent v1.8.0
	github.com/aws/aws-sdk-go-v2/service/devopsguru v1.41.7
	github.com/aws/aws-sdk-go-v2/service/directconnect v1.41.0
	github.com/aws/aws-sdk-go-v2/service/directoryservice v1.39.4
	github.com/aws/aws-sdk-go-v2/service/dlm v1.37.6
	github.com/aws/aws-sdk-go-v2/service/docdb v1.49.5
	github.com/aws/aws-sdk-go-v2/service/docdbelastic v1.21.7
	github.com/aws/aws-sdk-go-v2/service/dsql v1.14.7
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.59.0
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.34.0
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.308.0
	github.com/aws/aws-sdk-go-v2/service/ecr v1.58.4
	github.com/aws/aws-sdk-go-v2/service/ecrpublic v1.39.6
	github.com/aws/aws-sdk-go-v2/service/ecs v1.85.0
	github.com/aws/aws-sdk-go-v2/service/efs v1.42.1
	github.com/aws/aws-sdk-go-v2/service/eks v1.87.0
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.54.3
	github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk v1.35.4
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing v1.34.6
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.55.4
	github.com/aws/aws-sdk-go-v2/service/elementalinference v1.1.7
	github.com/aws/aws-sdk-go-v2/service/emr v1.61.1
	github.com/aws/aws-sdk-go-v2/service/emrcontainers v1.41.6
	github.com/aws/aws-sdk-go-v2/service/emrserverless v1.42.2
	github.com/aws/aws-sdk-go-v2/service/entityresolution v1.28.5
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.46.6
	github.com/aws/aws-sdk-go-v2/service/evs v1.10.7
	github.com/aws/aws-sdk-go-v2/service/finspace v1.34.8
	github.com/aws/aws-sdk-go-v2/service/firehose v1.44.0
	github.com/aws/aws-sdk-go-v2/service/fis v1.38.6
	github.com/aws/aws-sdk-go-v2/service/fms v1.45.6
	github.com/aws/aws-sdk-go-v2/service/forecast v1.42.6
	github.com/aws/aws-sdk-go-v2/service/frauddetector v1.42.6
	github.com/aws/aws-sdk-go-v2/service/fsx v1.66.6
	github.com/aws/aws-sdk-go-v2/service/gamelift v1.56.0
	github.com/aws/aws-sdk-go-v2/service/globalaccelerator v1.36.6
	github.com/aws/aws-sdk-go-v2/service/glue v1.146.0
	github.com/aws/aws-sdk-go-v2/service/grafana v1.36.4
	github.com/aws/aws-sdk-go-v2/service/greengrass v1.33.4
	github.com/aws/aws-sdk-go-v2/service/greengrassv2 v1.43.8
	github.com/aws/aws-sdk-go-v2/service/groundstation v1.43.4
	github.com/aws/aws-sdk-go-v2/service/guardduty v1.80.0
	github.com/aws/aws-sdk-go-v2/service/healthlake v1.39.0
	github.com/aws/aws-sdk-go-v2/service/iam v1.54.5
	github.com/aws/aws-sdk-go-v2/service/identitystore v1.37.7
	github.com/aws/aws-sdk-go-v2/service/imagebuilder v1.55.6
	github.com/aws/aws-sdk-go-v2/service/inspector2 v1.49.2
	github.com/aws/aws-sdk-go-v2/service/interconnect v1.1.2
	github.com/aws/aws-sdk-go-v2/service/internetmonitor v1.27.6
	github.com/aws/aws-sdk-go-v2/service/invoicing v1.11.7
	github.com/aws/aws-sdk-go-v2/service/iot v1.75.4
	github.com/aws/aws-sdk-go-v2/service/iotdeviceadvisor v1.37.7
	github.com/aws/aws-sdk-go-v2/service/iotfleetwise v1.32.6
	github.com/aws/aws-sdk-go-v2/service/iotsitewise v1.53.0
	github.com/aws/aws-sdk-go-v2/service/iottwinmaker v1.30.5
	github.com/aws/aws-sdk-go-v2/service/iotwireless v1.56.8
	github.com/aws/aws-sdk-go-v2/service/ivs v1.52.1
	github.com/aws/aws-sdk-go-v2/service/ivschat v1.22.7
	github.com/aws/aws-sdk-go-v2/service/ivsrealtime v1.35.7
	github.com/aws/aws-sdk-go-v2/service/kafka v1.54.0
	github.com/aws/aws-sdk-go-v2/service/kafkaconnect v1.31.8
	github.com/aws/aws-sdk-go-v2/service/kendra v1.61.3
	github.com/aws/aws-sdk-go-v2/service/kendraranking v1.16.6
	github.com/aws/aws-sdk-go-v2/service/keyspaces v1.26.5
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.44.2
	github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2 v1.38.6
	github.com/aws/aws-sdk-go-v2/service/kinesisvideo v1.34.5
	github.com/aws/aws-sdk-go-v2/service/kms v1.53.4
	github.com/aws/aws-sdk-go-v2/service/lakeformation v1.48.4
	github.com/aws/aws-sdk-go-v2/service/lambda v1.93.0
	github.com/aws/aws-sdk-go-v2/service/launchwizard v1.15.7
	github.com/aws/aws-sdk-go-v2/service/lexmodelsv2 v1.62.4
	github.com/aws/aws-sdk-go-v2/service/licensemanager v1.38.4
	github.com/aws/aws-sdk-go-v2/service/lightsail v1.56.1
	github.com/aws/aws-sdk-go-v2/service/location v1.52.8
	github.com/aws/aws-sdk-go-v2/service/lookoutequipment v1.37.7
	github.com/aws/aws-sdk-go-v2/service/m2 v1.27.6
	github.com/aws/aws-sdk-go-v2/service/macie2 v1.52.3
	github.com/aws/aws-sdk-go-v2/service/mailmanager v1.19.5
	github.com/aws/aws-sdk-go-v2/service/managedblockchain v1.32.3
	github.com/aws/aws-sdk-go-v2/service/mediaconnect v1.51.0
	github.com/aws/aws-sdk-go-v2/service/mediaconvert v1.93.1
	github.com/aws/aws-sdk-go-v2/service/medialive v1.99.0
	github.com/aws/aws-sdk-go-v2/service/mediapackage v1.40.1
	github.com/aws/aws-sdk-go-v2/service/mediapackagev2 v1.40.0
	github.com/aws/aws-sdk-go-v2/service/mediapackagevod v1.40.1
	github.com/aws/aws-sdk-go-v2/service/mediatailor v1.59.6
	github.com/aws/aws-sdk-go-v2/service/medicalimaging v1.26.7
	github.com/aws/aws-sdk-go-v2/service/memorydb v1.34.6
	github.com/aws/aws-sdk-go-v2/service/migrationhubrefactorspaces v1.26.7
	github.com/aws/aws-sdk-go-v2/service/mpa v1.8.7
	github.com/aws/aws-sdk-go-v2/service/mq v1.36.0
	github.com/aws/aws-sdk-go-v2/service/mwaa v1.41.5
	github.com/aws/aws-sdk-go-v2/service/mwaaserverless v1.1.2
	github.com/aws/aws-sdk-go-v2/service/neptune v1.46.0
	github.com/aws/aws-sdk-go-v2/service/neptunegraph v1.22.5
	github.com/aws/aws-sdk-go-v2/service/networkfirewall v1.61.8
	github.com/aws/aws-sdk-go-v2/service/networkmanager v1.42.6
	github.com/aws/aws-sdk-go-v2/service/notifications v1.8.6
	github.com/aws/aws-sdk-go-v2/service/notificationscontacts v1.6.6
	github.com/aws/aws-sdk-go-v2/service/novaact v1.1.7
	github.com/aws/aws-sdk-go-v2/service/oam v1.24.7
	github.com/aws/aws-sdk-go-v2/service/observabilityadmin v1.17.0
	github.com/aws/aws-sdk-go-v2/service/odb v1.12.0
	github.com/aws/aws-sdk-go-v2/service/omics v1.46.0
	github.com/aws/aws-sdk-go-v2/service/opensearch v1.72.0
	github.com/aws/aws-sdk-go-v2/service/opensearchserverless v1.32.1
	github.com/aws/aws-sdk-go-v2/service/organizations v1.51.10
	github.com/aws/aws-sdk-go-v2/service/osis v1.22.7
	github.com/aws/aws-sdk-go-v2/service/panorama v1.28.8
	github.com/aws/aws-sdk-go-v2/service/paymentcryptography v1.31.1
	github.com/aws/aws-sdk-go-v2/service/pcaconnectorad v1.16.7
	github.com/aws/aws-sdk-go-v2/service/pcaconnectorscep v1.12.7
	github.com/aws/aws-sdk-go-v2/service/pcs v1.20.6
	github.com/aws/aws-sdk-go-v2/service/personalize v1.48.4
	github.com/aws/aws-sdk-go-v2/service/pinpoint v1.40.3
	github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2 v1.29.7
	github.com/aws/aws-sdk-go-v2/service/pipes v1.24.6
	github.com/aws/aws-sdk-go-v2/service/proton v1.40.4
	github.com/aws/aws-sdk-go-v2/service/qbusiness v1.35.5
	github.com/aws/aws-sdk-go-v2/service/qconnect v1.32.8
	github.com/aws/aws-sdk-go-v2/service/quicksight v1.115.0
	github.com/aws/aws-sdk-go-v2/service/ram v1.37.3
	github.com/aws/aws-sdk-go-v2/service/rbin v1.28.7
	github.com/aws/aws-sdk-go-v2/service/rds v1.119.3
	github.com/aws/aws-sdk-go-v2/service/redshift v1.63.3
	github.com/aws/aws-sdk-go-v2/service/redshiftserverless v1.35.8
	github.com/aws/aws-sdk-go-v2/service/rekognition v1.52.3
	github.com/aws/aws-sdk-go-v2/service/resiliencehub v1.36.6
	github.com/aws/aws-sdk-go-v2/service/resourceexplorer2 v1.24.6
	github.com/aws/aws-sdk-go-v2/service/resourcegroups v1.34.2
	github.com/aws/aws-sdk-go-v2/service/rolesanywhere v1.23.7
	github.com/aws/aws-sdk-go-v2/service/route53 v1.63.3
	github.com/aws/aws-sdk-go-v2/service/route53globalresolver v1.3.7
	github.com/aws/aws-sdk-go-v2/service/route53profiles v1.10.3
	github.com/aws/aws-sdk-go-v2/service/route53recoverycontrolconfig v1.33.6
	github.com/aws/aws-sdk-go-v2/service/route53recoveryreadiness v1.27.2
	github.com/aws/aws-sdk-go-v2/service/route53resolver v1.46.0
	github.com/aws/aws-sdk-go-v2/service/rtbfabric v1.7.7
	github.com/aws/aws-sdk-go-v2/service/rum v1.31.8
	github.com/aws/aws-sdk-go-v2/service/s3 v1.104.0
	github.com/aws/aws-sdk-go-v2/service/s3control v1.71.5
	github.com/aws/aws-sdk-go-v2/service/s3files v1.1.1
	github.com/aws/aws-sdk-go-v2/service/s3outposts v1.35.2
	github.com/aws/aws-sdk-go-v2/service/s3tables v1.16.6
	github.com/aws/aws-sdk-go-v2/service/s3vectors v1.8.0
	github.com/aws/aws-sdk-go-v2/service/sagemaker v1.256.0
	github.com/aws/aws-sdk-go-v2/service/scheduler v1.18.7
	github.com/aws/aws-sdk-go-v2/service/schemas v1.35.6
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.42.3
	github.com/aws/aws-sdk-go-v2/service/securityagent v1.5.0
	github.com/aws/aws-sdk-go-v2/service/securityhub v1.71.7
	github.com/aws/aws-sdk-go-v2/service/securitylake v1.26.2
	github.com/aws/aws-sdk-go-v2/service/servicecatalog v1.40.4
	github.com/aws/aws-sdk-go-v2/service/servicecatalogappregistry v1.36.6
	github.com/aws/aws-sdk-go-v2/service/servicediscovery v1.40.6
	github.com/aws/aws-sdk-go-v2/service/servicequotas v1.35.7
	github.com/aws/aws-sdk-go-v2/service/ses v1.35.2
	github.com/aws/aws-sdk-go-v2/service/sesv2 v1.62.4
	github.com/aws/aws-sdk-go-v2/service/sfn v1.43.0
	github.com/aws/aws-sdk-go-v2/service/shield v1.35.3
	github.com/aws/aws-sdk-go-v2/service/signer v1.33.6
	github.com/aws/aws-sdk-go-v2/service/sns v1.40.1
	github.com/aws/aws-sdk-go-v2/service/sqs v1.44.0
	github.com/aws/aws-sdk-go-v2/service/ssm v1.69.3
	github.com/aws/aws-sdk-go-v2/service/ssmcontacts v1.32.8
	github.com/aws/aws-sdk-go-v2/service/ssmguiconnect v1.6.5
	github.com/aws/aws-sdk-go-v2/service/ssmincidents v1.40.6
	github.com/aws/aws-sdk-go-v2/service/ssmquicksetup v1.9.6
	github.com/aws/aws-sdk-go-v2/service/ssmsap v1.27.7
	github.com/aws/aws-sdk-go-v2/service/ssoadmin v1.39.7
	github.com/aws/aws-sdk-go-v2/service/sts v1.43.3
	github.com/aws/aws-sdk-go-v2/service/supportapp v1.19.7
	github.com/aws/aws-sdk-go-v2/service/synthetics v1.44.0
	github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb v1.20.6
	github.com/aws/aws-sdk-go-v2/service/timestreamquery v1.37.0
	github.com/aws/aws-sdk-go-v2/service/timestreamwrite v1.36.0
	github.com/aws/aws-sdk-go-v2/service/transfer v1.73.3
	github.com/aws/aws-sdk-go-v2/service/uxc v1.1.2
	github.com/aws/aws-sdk-go-v2/service/verifiedpermissions v1.34.6
	github.com/aws/aws-sdk-go-v2/service/voiceid v1.31.6
	github.com/aws/aws-sdk-go-v2/service/vpclattice v1.22.2
	github.com/aws/aws-sdk-go-v2/service/wafv2 v1.73.0
	github.com/aws/aws-sdk-go-v2/service/workspaces v1.70.0
	github.com/aws/aws-sdk-go-v2/service/workspacesinstances v1.6.5
	github.com/aws/aws-sdk-go-v2/service/workspacesthinclient v1.21.7
	github.com/aws/aws-sdk-go-v2/service/workspacesweb v1.40.7
	github.com/aws/aws-sdk-go-v2/service/xray v1.37.3
	github.com/aws/smithy-go v1.27.3
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/jmoiron/sqlx v1.4.0
	github.com/open-policy-agent/opa v1.18.0
	github.com/ory/dockertest/v3 v3.12.0
	github.com/spf13/cobra v1.10.2
	github.com/spf13/viper v1.21.0
	github.com/ulikunitz/xz v0.5.15
	golang.org/x/crypto v0.53.0
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.21.0
	golang.org/x/time v0.15.0
	google.golang.org/api v0.286.0
	modernc.org/sqlite v1.53.0
)

require (
	github.com/aws/aws-sdk-go-v2/service/appfabric v1.17.3
	github.com/aws/aws-sdk-go-v2/service/artifact v1.16.6
	github.com/aws/aws-sdk-go-v2/service/bcmdashboards v1.3.7
	github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation v1.16.7
	github.com/aws/aws-sdk-go-v2/service/chimesdkmediapipelines v1.27.9
	github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging v1.33.7
	github.com/aws/aws-sdk-go-v2/service/chimesdkvoice v1.29.3
	github.com/aws/aws-sdk-go-v2/service/clouddirectory v1.31.6
	github.com/aws/aws-sdk-go-v2/service/cloudhsmv2 v1.35.4
	github.com/aws/aws-sdk-go-v2/service/cloudsearch v1.33.5
	github.com/aws/aws-sdk-go-v2/service/dataexchange v1.42.7
	github.com/aws/aws-sdk-go-v2/service/datapipeline v1.31.6
	github.com/aws/aws-sdk-go-v2/service/devicefarm v1.39.6
	github.com/aws/aws-sdk-go-v2/service/drs v1.39.5
	github.com/aws/aws-sdk-go-v2/service/gameliftstreams v1.11.13
	github.com/aws/aws-sdk-go-v2/service/glacier v1.33.5
	github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations v1.10.8
	github.com/aws/aws-sdk-go-v2/service/licensemanagerlinuxsubscriptions v1.21.8
	github.com/aws/aws-sdk-go-v2/service/licensemanagerusersubscriptions v1.22.8
	github.com/aws/aws-sdk-go-v2/service/mgn v1.46.1
	github.com/aws/aws-sdk-go-v2/service/migrationhub v1.32.4
	github.com/aws/aws-sdk-go-v2/service/migrationhuborchestrator v1.19.7
	github.com/aws/aws-sdk-go-v2/service/networkflowmonitor v1.12.6
	github.com/aws/aws-sdk-go-v2/service/networkmonitor v1.14.7
)

require (
	cloud.google.com/go/auth v0.20.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	dario.cat/mergo v1.0.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.12.0 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20230124172434-306776ec8161 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.7.2 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/Nvveen/Gotty v0.0.0-20120604004816-cd527374f1e5 // indirect
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.13 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.30 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.22 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.12.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.29 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.29 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.2.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.31.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.36.6 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/containerd/continuity v0.4.5 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.1 // indirect
	github.com/docker/cli v27.4.1+incompatible // indirect
	github.com/docker/docker v27.1.1+incompatible // indirect
	github.com/docker/go-connections v0.5.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.17 // indirect
	github.com/googleapis/gax-go/v2 v2.22.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/lann/builder v0.0.0-20180802200727-47ae307949d0 // indirect
	github.com/lann/ps v0.0.0-20150810152359-62de8c46ede0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.4 // indirect
	github.com/lestrrat-go/dsig v1.3.0 // indirect
	github.com/lestrrat-go/dsig-secp256k1 v1.0.0 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc/v3 v3.0.6 // indirect
	github.com/lestrrat-go/jwx/v3 v3.1.1 // indirect
	github.com/lestrrat-go/option/v2 v2.0.0 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/sys/user v0.3.0 // indirect
	github.com/moby/term v0.5.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/opencontainers/runc v1.2.3 // indirect
	github.com/pelletier/go-toml/v2 v2.4.2 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20250401214520-65e299d6c5c9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/tchap/go-patricia/v2 v2.3.3 // indirect
	github.com/valyala/fastjson v1.6.10 // indirect
	github.com/vektah/gqlparser/v2 v2.5.35 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xeipuuv/gojsonschema v1.2.0 // indirect
	github.com/yashtewari/glob-intersection v0.2.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260622175928-b703f567277d // indirect
	google.golang.org/grpc v1.81.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	modernc.org/libc v1.73.5 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
