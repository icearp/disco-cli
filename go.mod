module github.com/icearp/disco-cli

go 1.25.12

require (
	cloud.google.com/go/cloudquotas v1.12.0
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
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/automation/armautomation v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/avs/armavs v1.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurearcdata/armazurearcdata v0.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurestackhci/armazurestackhci v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurestackhci/armazurestackhcivm v0.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/baremetalinfrastructure/armbaremetalinfrastructure v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/batch/armbatch v1.2.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/blueprint/armblueprint v0.7.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/certificateregistration/armcertificateregistration v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/chaos/armchaos v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cloudhealth/armcloudhealth v0.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices v1.8.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/communication/armcommunication/v2 v2.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6 v6.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/confidentialledger/armconfidentialledger v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/connectedcache/armconnectedcache v1.0.0
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
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/domainregistration/armdomainregistration v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/durabletask/armdurabletask v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/edgemarketplace/armedgemarketplace v0.0.0-20260805152818-a441e9024c5b
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
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mongocluster/armmongocluster v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor v0.13.0 // keep >= v0.13.0: v0.12.0 dropped the DiagnosticSettings 2021-05-01-preview API monitor_resolvers.go needs, v0.13.0 restored it. See azure/CLAUDE.md.
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
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sqlvirtualmachine/armsqlvirtualmachine v1.0.0
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
	github.com/aws/aws-sdk-go-v2 v1.43.3
	github.com/aws/aws-sdk-go-v2/config v1.32.34
	github.com/aws/aws-sdk-go-v2/credentials v1.19.33
	github.com/aws/aws-sdk-go-v2/feature/rds/auth v1.6.34
	github.com/aws/aws-sdk-go-v2/service/accessanalyzer v1.51.3
	github.com/aws/aws-sdk-go-v2/service/acm v1.43.3
	github.com/aws/aws-sdk-go-v2/service/acmpca v1.49.3
	github.com/aws/aws-sdk-go-v2/service/aiops v1.9.3
	github.com/aws/aws-sdk-go-v2/service/amp v1.48.0
	github.com/aws/aws-sdk-go-v2/service/amplify v1.41.3
	github.com/aws/aws-sdk-go-v2/service/amplifyuibuilder v1.31.3
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.42.3
	github.com/aws/aws-sdk-go-v2/service/apigatewayv2 v1.37.3
	github.com/aws/aws-sdk-go-v2/service/appconfig v1.48.3
	github.com/aws/aws-sdk-go-v2/service/appfabric v1.19.3
	github.com/aws/aws-sdk-go-v2/service/appflow v1.54.3
	github.com/aws/aws-sdk-go-v2/service/appintegrations v1.40.3
	github.com/aws/aws-sdk-go-v2/service/applicationautoscaling v1.45.3
	github.com/aws/aws-sdk-go-v2/service/applicationinsights v1.38.3
	github.com/aws/aws-sdk-go-v2/service/applicationsignals v1.25.3
	github.com/aws/aws-sdk-go-v2/service/appmesh v1.38.3
	github.com/aws/aws-sdk-go-v2/service/apprunner v1.42.3
	github.com/aws/aws-sdk-go-v2/service/appstream v1.64.4
	github.com/aws/aws-sdk-go-v2/service/appsync v1.56.3
	github.com/aws/aws-sdk-go-v2/service/arcregionswitch v1.13.2
	github.com/aws/aws-sdk-go-v2/service/arczonalshift v1.25.3
	github.com/aws/aws-sdk-go-v2/service/artifact v1.20.3
	github.com/aws/aws-sdk-go-v2/service/athena v1.60.3
	github.com/aws/aws-sdk-go-v2/service/auditmanager v1.49.3
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.70.3
	github.com/aws/aws-sdk-go-v2/service/autoscalingplans v1.33.3
	github.com/aws/aws-sdk-go-v2/service/b2bi v1.0.0-preview.118
	github.com/aws/aws-sdk-go-v2/service/backup v1.59.3
	github.com/aws/aws-sdk-go-v2/service/backupgateway v1.30.3
	github.com/aws/aws-sdk-go-v2/service/batch v1.68.3
	github.com/aws/aws-sdk-go-v2/service/bcmdashboards v1.5.3
	github.com/aws/aws-sdk-go-v2/service/bcmdataexports v1.19.3
	github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator v1.15.1
	github.com/aws/aws-sdk-go-v2/service/bedrock v1.66.3
	github.com/aws/aws-sdk-go-v2/service/bedrockagent v1.58.3
	github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol v1.53.1
	github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation v1.18.3
	github.com/aws/aws-sdk-go-v2/service/billing v1.14.0
	github.com/aws/aws-sdk-go-v2/service/billingconductor v1.32.3
	github.com/aws/aws-sdk-go-v2/service/braket v1.43.3
	github.com/aws/aws-sdk-go-v2/service/budgets v1.46.3
	github.com/aws/aws-sdk-go-v2/service/chatbot v1.17.3
	github.com/aws/aws-sdk-go-v2/service/chimesdkidentity v1.30.3
	github.com/aws/aws-sdk-go-v2/service/chimesdkmediapipelines v1.29.3
	github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging v1.35.3
	github.com/aws/aws-sdk-go-v2/service/chimesdkvoice v1.32.3
	github.com/aws/aws-sdk-go-v2/service/cleanrooms v1.49.3
	github.com/aws/aws-sdk-go-v2/service/cleanroomsml v1.27.3
	github.com/aws/aws-sdk-go-v2/service/cloud9 v1.36.3
	github.com/aws/aws-sdk-go-v2/service/clouddirectory v1.33.3
	github.com/aws/aws-sdk-go-v2/service/cloudformation v1.76.0
	github.com/aws/aws-sdk-go-v2/service/cloudfront v1.67.3
	github.com/aws/aws-sdk-go-v2/service/cloudhsmv2 v1.37.3
	github.com/aws/aws-sdk-go-v2/service/cloudsearch v1.35.3
	github.com/aws/aws-sdk-go-v2/service/cloudtrail v1.58.3
	github.com/aws/aws-sdk-go-v2/service/cloudwatch v1.66.2
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.81.0
	github.com/aws/aws-sdk-go-v2/service/codeartifact v1.41.3
	github.com/aws/aws-sdk-go-v2/service/codebuild v1.72.3
	github.com/aws/aws-sdk-go-v2/service/codecommit v1.36.3
	github.com/aws/aws-sdk-go-v2/service/codedeploy v1.38.3
	github.com/aws/aws-sdk-go-v2/service/codeguruprofiler v1.32.3
	github.com/aws/aws-sdk-go-v2/service/codegurureviewer v1.37.3
	github.com/aws/aws-sdk-go-v2/service/codepipeline v1.49.3
	github.com/aws/aws-sdk-go-v2/service/codestarconnections v1.38.3
	github.com/aws/aws-sdk-go-v2/service/codestarnotifications v1.34.3
	github.com/aws/aws-sdk-go-v2/service/cognitoidentity v1.36.3
	github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider v1.67.3
	github.com/aws/aws-sdk-go-v2/service/comprehend v1.43.3
	github.com/aws/aws-sdk-go-v2/service/configservice v1.68.3
	github.com/aws/aws-sdk-go-v2/service/connect v1.184.3
	github.com/aws/aws-sdk-go-v2/service/connectcampaigns v1.23.3
	github.com/aws/aws-sdk-go-v2/service/connectcampaignsv2 v1.18.0
	github.com/aws/aws-sdk-go-v2/service/connectcases v1.44.3
	github.com/aws/aws-sdk-go-v2/service/controltower v1.31.3
	github.com/aws/aws-sdk-go-v2/service/costandusagereportservice v1.37.3
	github.com/aws/aws-sdk-go-v2/service/costexplorer v1.67.3
	github.com/aws/aws-sdk-go-v2/service/customerprofiles v1.65.3
	github.com/aws/aws-sdk-go-v2/service/databasemigrationservice v1.66.3
	github.com/aws/aws-sdk-go-v2/service/databrew v1.42.3
	github.com/aws/aws-sdk-go-v2/service/dataexchange v1.44.3
	github.com/aws/aws-sdk-go-v2/service/datapipeline v1.33.3
	github.com/aws/aws-sdk-go-v2/service/datasync v1.61.3
	github.com/aws/aws-sdk-go-v2/service/datazone v1.68.0
	github.com/aws/aws-sdk-go-v2/service/dax v1.32.3
	github.com/aws/aws-sdk-go-v2/service/deadline v1.35.3
	github.com/aws/aws-sdk-go-v2/service/detective v1.41.3
	github.com/aws/aws-sdk-go-v2/service/devicefarm v1.41.3
	github.com/aws/aws-sdk-go-v2/service/devopsagent v1.10.3
	github.com/aws/aws-sdk-go-v2/service/devopsguru v1.43.3
	github.com/aws/aws-sdk-go-v2/service/directconnect v1.44.0
	github.com/aws/aws-sdk-go-v2/service/directoryservice v1.41.3
	github.com/aws/aws-sdk-go-v2/service/dlm v1.39.3
	github.com/aws/aws-sdk-go-v2/service/docdb v1.51.3
	github.com/aws/aws-sdk-go-v2/service/docdbelastic v1.23.3
	github.com/aws/aws-sdk-go-v2/service/drs v1.42.4
	github.com/aws/aws-sdk-go-v2/service/dsql v1.16.4
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.63.0
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.36.3
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.319.0
	github.com/aws/aws-sdk-go-v2/service/ecr v1.60.3
	github.com/aws/aws-sdk-go-v2/service/ecrpublic v1.41.3
	github.com/aws/aws-sdk-go-v2/service/ecs v1.89.3
	github.com/aws/aws-sdk-go-v2/service/efs v1.44.3
	github.com/aws/aws-sdk-go-v2/service/eks v1.90.3
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.56.3
	github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk v1.37.3
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing v1.36.3
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.58.4
	github.com/aws/aws-sdk-go-v2/service/elementalinference v1.5.0
	github.com/aws/aws-sdk-go-v2/service/emr v1.64.3
	github.com/aws/aws-sdk-go-v2/service/emrcontainers v1.45.3
	github.com/aws/aws-sdk-go-v2/service/emrserverless v1.44.3
	github.com/aws/aws-sdk-go-v2/service/entityresolution v1.30.3
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.48.3
	github.com/aws/aws-sdk-go-v2/service/evs v1.13.3
	github.com/aws/aws-sdk-go-v2/service/finspace v1.36.3
	github.com/aws/aws-sdk-go-v2/service/firehose v1.46.3
	github.com/aws/aws-sdk-go-v2/service/fis v1.40.3
	github.com/aws/aws-sdk-go-v2/service/fms v1.47.3
	github.com/aws/aws-sdk-go-v2/service/forecast v1.44.3
	github.com/aws/aws-sdk-go-v2/service/frauddetector v1.44.3
	github.com/aws/aws-sdk-go-v2/service/fsx v1.68.3
	github.com/aws/aws-sdk-go-v2/service/gamelift v1.60.2
	github.com/aws/aws-sdk-go-v2/service/gameliftstreams v1.17.1
	github.com/aws/aws-sdk-go-v2/service/glacier v1.35.3
	github.com/aws/aws-sdk-go-v2/service/globalaccelerator v1.38.3
	github.com/aws/aws-sdk-go-v2/service/glue v1.151.1
	github.com/aws/aws-sdk-go-v2/service/grafana v1.38.3
	github.com/aws/aws-sdk-go-v2/service/greengrass v1.35.3
	github.com/aws/aws-sdk-go-v2/service/greengrassv2 v1.45.3
	github.com/aws/aws-sdk-go-v2/service/groundstation v1.45.3
	github.com/aws/aws-sdk-go-v2/service/guardduty v1.85.3
	github.com/aws/aws-sdk-go-v2/service/healthlake v1.42.3
	github.com/aws/aws-sdk-go-v2/service/iam v1.58.0
	github.com/aws/aws-sdk-go-v2/service/identitystore v1.39.3
	github.com/aws/aws-sdk-go-v2/service/imagebuilder v1.58.3
	github.com/aws/aws-sdk-go-v2/service/inspector2 v1.54.0
	github.com/aws/aws-sdk-go-v2/service/interconnect v1.4.2
	github.com/aws/aws-sdk-go-v2/service/internetmonitor v1.29.3
	github.com/aws/aws-sdk-go-v2/service/invoicing v1.13.3
	github.com/aws/aws-sdk-go-v2/service/iot v1.77.3
	github.com/aws/aws-sdk-go-v2/service/iotdeviceadvisor v1.39.3
	github.com/aws/aws-sdk-go-v2/service/iotfleetwise v1.34.3
	github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations v1.12.3
	github.com/aws/aws-sdk-go-v2/service/iotsitewise v1.56.1
	github.com/aws/aws-sdk-go-v2/service/iottwinmaker v1.32.3
	github.com/aws/aws-sdk-go-v2/service/iotwireless v1.59.3
	github.com/aws/aws-sdk-go-v2/service/ivs v1.55.3
	github.com/aws/aws-sdk-go-v2/service/ivschat v1.24.3
	github.com/aws/aws-sdk-go-v2/service/ivsrealtime v1.37.3
	github.com/aws/aws-sdk-go-v2/service/kafka v1.57.1
	github.com/aws/aws-sdk-go-v2/service/kafkaconnect v1.33.3
	github.com/aws/aws-sdk-go-v2/service/kendra v1.63.3
	github.com/aws/aws-sdk-go-v2/service/kendraranking v1.19.3
	github.com/aws/aws-sdk-go-v2/service/keyspaces v1.28.3
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.46.3
	github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2 v1.41.3
	github.com/aws/aws-sdk-go-v2/service/kinesisvideo v1.36.3
	github.com/aws/aws-sdk-go-v2/service/kms v1.55.3
	github.com/aws/aws-sdk-go-v2/service/lakeformation v1.50.3
	github.com/aws/aws-sdk-go-v2/service/lambda v1.101.1
	github.com/aws/aws-sdk-go-v2/service/launchwizard v1.17.3
	github.com/aws/aws-sdk-go-v2/service/lexmodelsv2 v1.64.3
	github.com/aws/aws-sdk-go-v2/service/licensemanager v1.41.3
	github.com/aws/aws-sdk-go-v2/service/licensemanagerlinuxsubscriptions v1.23.3
	github.com/aws/aws-sdk-go-v2/service/licensemanagerusersubscriptions v1.24.3
	github.com/aws/aws-sdk-go-v2/service/lightsail v1.58.3
	github.com/aws/aws-sdk-go-v2/service/location v1.54.3
	github.com/aws/aws-sdk-go-v2/service/lookoutequipment v1.39.3
	github.com/aws/aws-sdk-go-v2/service/m2 v1.29.3
	github.com/aws/aws-sdk-go-v2/service/macie2 v1.54.3
	github.com/aws/aws-sdk-go-v2/service/mailmanager v1.21.3
	github.com/aws/aws-sdk-go-v2/service/managedblockchain v1.34.3
	github.com/aws/aws-sdk-go-v2/service/mediaconnect v1.53.3
	github.com/aws/aws-sdk-go-v2/service/mediaconvert v1.97.0
	github.com/aws/aws-sdk-go-v2/service/medialive v1.101.3
	github.com/aws/aws-sdk-go-v2/service/mediapackage v1.42.3
	github.com/aws/aws-sdk-go-v2/service/mediapackagev2 v1.43.3
	github.com/aws/aws-sdk-go-v2/service/mediapackagevod v1.42.3
	github.com/aws/aws-sdk-go-v2/service/mediatailor v1.63.3
	github.com/aws/aws-sdk-go-v2/service/medicalimaging v1.28.3
	github.com/aws/aws-sdk-go-v2/service/memorydb v1.36.3
	github.com/aws/aws-sdk-go-v2/service/mgn v1.48.3
	github.com/aws/aws-sdk-go-v2/service/migrationhub v1.34.3
	github.com/aws/aws-sdk-go-v2/service/migrationhuborchestrator v1.21.3
	github.com/aws/aws-sdk-go-v2/service/migrationhubrefactorspaces v1.28.3
	github.com/aws/aws-sdk-go-v2/service/mpa v1.10.3
	github.com/aws/aws-sdk-go-v2/service/mq v1.39.3
	github.com/aws/aws-sdk-go-v2/service/mwaa v1.43.3
	github.com/aws/aws-sdk-go-v2/service/mwaaserverless v1.3.3
	github.com/aws/aws-sdk-go-v2/service/neptune v1.48.3
	github.com/aws/aws-sdk-go-v2/service/neptunegraph v1.24.3
	github.com/aws/aws-sdk-go-v2/service/networkfirewall v1.67.0
	github.com/aws/aws-sdk-go-v2/service/networkflowmonitor v1.14.3
	github.com/aws/aws-sdk-go-v2/service/networkmanager v1.44.3
	github.com/aws/aws-sdk-go-v2/service/networkmonitor v1.16.3
	github.com/aws/aws-sdk-go-v2/service/notifications v1.10.3
	github.com/aws/aws-sdk-go-v2/service/notificationscontacts v1.8.3
	github.com/aws/aws-sdk-go-v2/service/novaact v1.3.3
	github.com/aws/aws-sdk-go-v2/service/oam v1.26.3
	github.com/aws/aws-sdk-go-v2/service/observabilityadmin v1.22.0
	github.com/aws/aws-sdk-go-v2/service/odb v1.15.4
	github.com/aws/aws-sdk-go-v2/service/omics v1.49.4
	github.com/aws/aws-sdk-go-v2/service/opensearch v1.75.3
	github.com/aws/aws-sdk-go-v2/service/opensearchserverless v1.34.3
	github.com/aws/aws-sdk-go-v2/service/organizations v1.53.4
	github.com/aws/aws-sdk-go-v2/service/osis v1.24.3
	github.com/aws/aws-sdk-go-v2/service/outposts v1.66.0
	github.com/aws/aws-sdk-go-v2/service/paymentcryptography v1.33.3
	github.com/aws/aws-sdk-go-v2/service/pcaconnectorad v1.18.3
	github.com/aws/aws-sdk-go-v2/service/pcaconnectorscep v1.14.3
	github.com/aws/aws-sdk-go-v2/service/pcs v1.24.3
	github.com/aws/aws-sdk-go-v2/service/personalize v1.50.3
	github.com/aws/aws-sdk-go-v2/service/pinpoint v1.42.3
	github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2 v1.32.3
	github.com/aws/aws-sdk-go-v2/service/pipes v1.26.3
	github.com/aws/aws-sdk-go-v2/service/polly v1.60.3
	github.com/aws/aws-sdk-go-v2/service/proton v1.42.3
	github.com/aws/aws-sdk-go-v2/service/qapps v1.14.3
	github.com/aws/aws-sdk-go-v2/service/qbusiness v1.37.3
	github.com/aws/aws-sdk-go-v2/service/qconnect v1.34.3
	github.com/aws/aws-sdk-go-v2/service/quicksight v1.123.0
	github.com/aws/aws-sdk-go-v2/service/ram v1.39.3
	github.com/aws/aws-sdk-go-v2/service/rbin v1.30.3
	github.com/aws/aws-sdk-go-v2/service/rds v1.124.0
	github.com/aws/aws-sdk-go-v2/service/redshift v1.65.3
	github.com/aws/aws-sdk-go-v2/service/redshiftserverless v1.38.4
	github.com/aws/aws-sdk-go-v2/service/rekognition v1.54.3
	github.com/aws/aws-sdk-go-v2/service/repostspace v1.17.3
	github.com/aws/aws-sdk-go-v2/service/resiliencehub v1.38.3
	github.com/aws/aws-sdk-go-v2/service/resourceexplorer2 v1.27.3
	github.com/aws/aws-sdk-go-v2/service/resourcegroups v1.36.3
	github.com/aws/aws-sdk-go-v2/service/rolesanywhere v1.26.2
	github.com/aws/aws-sdk-go-v2/service/route53 v1.65.5
	github.com/aws/aws-sdk-go-v2/service/route53globalresolver v1.6.3
	github.com/aws/aws-sdk-go-v2/service/route53profiles v1.12.3
	github.com/aws/aws-sdk-go-v2/service/route53recoverycontrolconfig v1.35.3
	github.com/aws/aws-sdk-go-v2/service/route53recoveryreadiness v1.29.3
	github.com/aws/aws-sdk-go-v2/service/route53resolver v1.48.3
	github.com/aws/aws-sdk-go-v2/service/rtbfabric v1.10.3
	github.com/aws/aws-sdk-go-v2/service/rum v1.33.3
	github.com/aws/aws-sdk-go-v2/service/s3 v1.106.4
	github.com/aws/aws-sdk-go-v2/service/s3control v1.73.3
	github.com/aws/aws-sdk-go-v2/service/s3files v1.3.3
	github.com/aws/aws-sdk-go-v2/service/s3outposts v1.37.3
	github.com/aws/aws-sdk-go-v2/service/s3tables v1.18.3
	github.com/aws/aws-sdk-go-v2/service/s3vectors v1.10.3
	github.com/aws/aws-sdk-go-v2/service/sagemaker v1.263.1
	github.com/aws/aws-sdk-go-v2/service/sagemakergeospatial v1.22.3
	github.com/aws/aws-sdk-go-v2/service/savingsplans v1.35.3
	github.com/aws/aws-sdk-go-v2/service/scheduler v1.20.3
	github.com/aws/aws-sdk-go-v2/service/schemas v1.37.3
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.44.3
	github.com/aws/aws-sdk-go-v2/service/securityagent v1.9.1
	github.com/aws/aws-sdk-go-v2/service/securityhub v1.75.3
	github.com/aws/aws-sdk-go-v2/service/securityir v1.13.3
	github.com/aws/aws-sdk-go-v2/service/securitylake v1.28.3
	github.com/aws/aws-sdk-go-v2/service/serverlessapplicationrepository v1.33.3
	github.com/aws/aws-sdk-go-v2/service/servicecatalog v1.42.3
	github.com/aws/aws-sdk-go-v2/service/servicecatalogappregistry v1.38.3
	github.com/aws/aws-sdk-go-v2/service/servicediscovery v1.43.3
	github.com/aws/aws-sdk-go-v2/service/servicequotas v1.37.3
	github.com/aws/aws-sdk-go-v2/service/ses v1.37.3
	github.com/aws/aws-sdk-go-v2/service/sesv2 v1.66.3
	github.com/aws/aws-sdk-go-v2/service/sfn v1.45.3
	github.com/aws/aws-sdk-go-v2/service/shield v1.37.3
	github.com/aws/aws-sdk-go-v2/service/signer v1.35.3
	github.com/aws/aws-sdk-go-v2/service/snowdevicemanagement v1.28.3
	github.com/aws/aws-sdk-go-v2/service/sns v1.42.3
	github.com/aws/aws-sdk-go-v2/service/socialmessaging v1.13.3
	github.com/aws/aws-sdk-go-v2/service/sqs v1.46.3
	github.com/aws/aws-sdk-go-v2/service/ssm v1.73.3
	github.com/aws/aws-sdk-go-v2/service/ssmcontacts v1.34.3
	github.com/aws/aws-sdk-go-v2/service/ssmguiconnect v1.8.3
	github.com/aws/aws-sdk-go-v2/service/ssmincidents v1.42.3
	github.com/aws/aws-sdk-go-v2/service/ssmquicksetup v1.11.3
	github.com/aws/aws-sdk-go-v2/service/ssmsap v1.29.3
	github.com/aws/aws-sdk-go-v2/service/ssoadmin v1.43.0
	github.com/aws/aws-sdk-go-v2/service/storagegateway v1.46.3
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.3
	github.com/aws/aws-sdk-go-v2/service/supplychain v1.20.3
	github.com/aws/aws-sdk-go-v2/service/supportapp v1.21.3
	github.com/aws/aws-sdk-go-v2/service/swf v1.37.3
	github.com/aws/aws-sdk-go-v2/service/synthetics v1.47.3
	github.com/aws/aws-sdk-go-v2/service/textract v1.43.3
	github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb v1.23.0
	github.com/aws/aws-sdk-go-v2/service/timestreamquery v1.39.3
	github.com/aws/aws-sdk-go-v2/service/timestreamwrite v1.38.3
	github.com/aws/aws-sdk-go-v2/service/tnb v1.21.3
	github.com/aws/aws-sdk-go-v2/service/transcribe v1.58.3
	github.com/aws/aws-sdk-go-v2/service/transfer v1.75.3
	github.com/aws/aws-sdk-go-v2/service/translate v1.36.3
	github.com/aws/aws-sdk-go-v2/service/trustedadvisor v1.18.2
	github.com/aws/aws-sdk-go-v2/service/uxc v1.3.3
	github.com/aws/aws-sdk-go-v2/service/verifiedpermissions v1.36.3
	github.com/aws/aws-sdk-go-v2/service/voiceid v1.33.3
	github.com/aws/aws-sdk-go-v2/service/vpclattice v1.25.4
	github.com/aws/aws-sdk-go-v2/service/waf v1.33.3
	github.com/aws/aws-sdk-go-v2/service/wafregional v1.33.3
	github.com/aws/aws-sdk-go-v2/service/wafv2 v1.77.2
	github.com/aws/aws-sdk-go-v2/service/wellarchitected v1.42.3
	github.com/aws/aws-sdk-go-v2/service/workmail v1.39.3
	github.com/aws/aws-sdk-go-v2/service/workspaces v1.73.0
	github.com/aws/aws-sdk-go-v2/service/workspacesinstances v1.9.3
	github.com/aws/aws-sdk-go-v2/service/workspacesthinclient v1.23.3
	github.com/aws/aws-sdk-go-v2/service/workspacesweb v1.42.3
	github.com/aws/aws-sdk-go-v2/service/xray v1.39.3
	github.com/aws/smithy-go v1.27.6
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/jmoiron/sqlx v1.4.0
	github.com/open-policy-agent/opa v1.19.0 // keep >= v1.19.0: earlier releases make text/template reachable and kill linker DCE (see CLAUDE.md).
	github.com/spf13/cobra v1.10.2
	github.com/spf13/viper v1.21.0
	github.com/ulikunitz/xz v0.5.16
	golang.org/x/crypto v0.54.0
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.22.0
	golang.org/x/time v0.15.0
	google.golang.org/api v0.292.0
	google.golang.org/protobuf v1.36.11
	modernc.org/sqlite v1.56.0
)

require (
	cloud.google.com/go/auth v0.22.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.12.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.8.0 // indirect
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.16 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.35 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.27 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.12.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.35 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.20 // indirect
	github.com/googleapis/gax-go/v2 v2.23.0 // indirect
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
	github.com/lestrrat-go/jwx/v3 v3.2.0 // indirect
	github.com/lestrrat-go/option/v2 v2.0.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
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
	github.com/vektah/gqlparser/v2 v2.5.36 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/yashtewari/glob-intersection v0.2.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.67.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.70.0 // indirect
	go.opentelemetry.io/otel v1.45.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/trace v1.45.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260630182238-925bb5da69e7 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/grpc v1.83.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
