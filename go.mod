module codeberg.org/icearp/disco

go 1.25.0

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.21.0
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.13.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2 v2.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6 v6.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6 v6.6.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datafactory/armdatafactory v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2 v2.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault v1.5.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/logic/armlogic v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor v0.11.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6 v6.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/security/armsecurity v0.14.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage v1.8.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/synapse/armsynapse v0.8.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trafficmanager/armtrafficmanager v1.3.0
	github.com/Masterminds/squirrel v1.5.4
	github.com/adrg/xdg v0.5.3
	github.com/aws/aws-sdk-go-v2 v1.41.7
	github.com/aws/aws-sdk-go-v2/config v1.32.14
	github.com/aws/aws-sdk-go-v2/credentials v1.19.14
	github.com/aws/aws-sdk-go-v2/service/accessanalyzer v1.47.2
	github.com/aws/aws-sdk-go-v2/service/acm v1.38.2
	github.com/aws/aws-sdk-go-v2/service/acmpca v1.46.13
	github.com/aws/aws-sdk-go-v2/service/aiops v1.6.23
	github.com/aws/aws-sdk-go-v2/service/amp v1.42.11
	github.com/aws/aws-sdk-go-v2/service/amplify v1.38.16
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.39.1
	github.com/aws/aws-sdk-go-v2/service/apigatewayv2 v1.34.1
	github.com/aws/aws-sdk-go-v2/service/appflow v1.51.14
	github.com/aws/aws-sdk-go-v2/service/appintegrations v1.37.9
	github.com/aws/aws-sdk-go-v2/service/applicationautoscaling v1.41.16
	github.com/aws/aws-sdk-go-v2/service/applicationinsights v1.34.22
	github.com/aws/aws-sdk-go-v2/service/applicationsignals v1.21.1
	github.com/aws/aws-sdk-go-v2/service/apprunner v1.39.15
	github.com/aws/aws-sdk-go-v2/service/arcregionswitch v1.6.5
	github.com/aws/aws-sdk-go-v2/service/arczonalshift v1.22.25
	github.com/aws/aws-sdk-go-v2/service/athena v1.57.5
	github.com/aws/aws-sdk-go-v2/service/auditmanager v1.46.13
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.66.2
	github.com/aws/aws-sdk-go-v2/service/autoscalingplans v1.30.16
	github.com/aws/aws-sdk-go-v2/service/backup v1.55.1
	github.com/aws/aws-sdk-go-v2/service/backupgateway v1.26.5
	github.com/aws/aws-sdk-go-v2/service/batch v1.64.0
	github.com/aws/aws-sdk-go-v2/service/bcmdataexports v1.14.2
	github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator v1.10.11
	github.com/aws/aws-sdk-go-v2/service/bedrock v1.59.2
	github.com/aws/aws-sdk-go-v2/service/bedrockagent v1.53.2
	github.com/aws/aws-sdk-go-v2/service/billing v1.10.6
	github.com/aws/aws-sdk-go-v2/service/billingconductor v1.28.7
	github.com/aws/aws-sdk-go-v2/service/braket v1.40.2
	github.com/aws/aws-sdk-go-v2/service/cloudformation v1.71.9
	github.com/aws/aws-sdk-go-v2/service/cloudfront v1.61.0
	github.com/aws/aws-sdk-go-v2/service/cloudtrail v1.55.10
	github.com/aws/aws-sdk-go-v2/service/cloudwatch v1.56.0
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.68.0
	github.com/aws/aws-sdk-go-v2/service/cognitoidentity v1.33.23
	github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider v1.60.1
	github.com/aws/aws-sdk-go-v2/service/configservice v1.62.2
	github.com/aws/aws-sdk-go-v2/service/connect v1.172.1
	github.com/aws/aws-sdk-go-v2/service/controltower v1.28.10
	github.com/aws/aws-sdk-go-v2/service/detective v1.38.14
	github.com/aws/aws-sdk-go-v2/service/docdb v1.48.14
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.57.1
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.296.2
	github.com/aws/aws-sdk-go-v2/service/ecr v1.56.2
	github.com/aws/aws-sdk-go-v2/service/ecs v1.76.0
	github.com/aws/aws-sdk-go-v2/service/efs v1.41.15
	github.com/aws/aws-sdk-go-v2/service/eks v1.81.2
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.52.0
	github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk v1.34.3
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing v1.33.23
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.54.10
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.45.24
	github.com/aws/aws-sdk-go-v2/service/firehose v1.42.14
	github.com/aws/aws-sdk-go-v2/service/glue v1.139.3
	github.com/aws/aws-sdk-go-v2/service/guardduty v1.75.2
	github.com/aws/aws-sdk-go-v2/service/iam v1.53.7
	github.com/aws/aws-sdk-go-v2/service/identitystore v1.36.6
	github.com/aws/aws-sdk-go-v2/service/inspector2 v1.47.5
	github.com/aws/aws-sdk-go-v2/service/iot v1.72.8
	github.com/aws/aws-sdk-go-v2/service/kafka v1.50.0
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.43.6
	github.com/aws/aws-sdk-go-v2/service/kms v1.50.5
	github.com/aws/aws-sdk-go-v2/service/lakeformation v1.47.7
	github.com/aws/aws-sdk-go-v2/service/lambda v1.88.5
	github.com/aws/aws-sdk-go-v2/service/lightsail v1.53.2
	github.com/aws/aws-sdk-go-v2/service/macie2 v1.51.1
	github.com/aws/aws-sdk-go-v2/service/mq v1.34.21
	github.com/aws/aws-sdk-go-v2/service/neptune v1.44.4
	github.com/aws/aws-sdk-go-v2/service/networkfirewall v1.60.0
	github.com/aws/aws-sdk-go-v2/service/opensearch v1.66.0
	github.com/aws/aws-sdk-go-v2/service/organizations v1.51.2
	github.com/aws/aws-sdk-go-v2/service/rds v1.117.1
	github.com/aws/aws-sdk-go-v2/service/redshift v1.62.6
	github.com/aws/aws-sdk-go-v2/service/route53 v1.62.5
	github.com/aws/aws-sdk-go-v2/service/s3 v1.98.0
	github.com/aws/aws-sdk-go-v2/service/s3control v1.69.0
	github.com/aws/aws-sdk-go-v2/service/sagemaker v1.244.0
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.41.6
	github.com/aws/aws-sdk-go-v2/service/securityhub v1.69.1
	github.com/aws/aws-sdk-go-v2/service/servicecatalog v1.39.13
	github.com/aws/aws-sdk-go-v2/service/sesv2 v1.60.3
	github.com/aws/aws-sdk-go-v2/service/sfn v1.40.11
	github.com/aws/aws-sdk-go-v2/service/shield v1.34.22
	github.com/aws/aws-sdk-go-v2/service/sns v1.39.15
	github.com/aws/aws-sdk-go-v2/service/sqs v1.42.25
	github.com/aws/aws-sdk-go-v2/service/ssm v1.68.5
	github.com/aws/aws-sdk-go-v2/service/ssoadmin v1.37.7
	github.com/aws/aws-sdk-go-v2/service/sts v1.41.10
	github.com/aws/aws-sdk-go-v2/service/wafv2 v1.71.4
	github.com/aws/smithy-go v1.25.1
	github.com/jmoiron/sqlx v1.4.0
	github.com/microsoft/kiota-abstractions-go v1.9.4
	github.com/microsoftgraph/msgraph-sdk-go v1.97.0
	github.com/microsoftgraph/msgraph-sdk-go-core v1.4.0
	github.com/open-policy-agent/opa v1.15.2
	github.com/spf13/cobra v1.10.2
	github.com/spf13/viper v1.21.0
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.20.0
	google.golang.org/api v0.274.0
	modernc.org/sqlite v1.48.0
)

require (
	cloud.google.com/go/auth v0.18.2 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.11.2 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.6.0 // indirect
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.10 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/appstream v1.58.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/appsync v1.53.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol v1.34.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/databasemigrationservice v1.62.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/datasync v1.58.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/datazone v1.59.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/deadline v1.31.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/gamelift v1.54.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/greengrass v1.32.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.11.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.22 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/iotsitewise v1.52.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/iotwireless v1.55.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ivs v1.50.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ivsrealtime v1.34.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/mailmanager v1.18.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/mediaconnect v1.48.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/medialive v1.95.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/networkmanager v1.41.10 // indirect
	github.com/aws/aws-sdk-go-v2/service/pinpoint v1.39.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/qconnect v1.30.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/quicksight v1.108.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/route53resolver v1.42.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/ses v1.34.24 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/vpclattice v1.20.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/workspacesweb v1.39.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.14 // indirect
	github.com/googleapis/gax-go/v2 v2.19.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/lann/builder v0.0.0-20180802200727-47ae307949d0 // indirect
	github.com/lann/ps v0.0.0-20150810152359-62de8c46ede0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.4 // indirect
	github.com/lestrrat-go/dsig v1.0.0 // indirect
	github.com/lestrrat-go/dsig-secp256k1 v1.0.0 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc/v3 v3.0.2 // indirect
	github.com/lestrrat-go/jwx/v3 v3.0.13 // indirect
	github.com/lestrrat-go/option/v2 v2.0.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/microsoft/kiota-authentication-azure-go v1.3.1 // indirect
	github.com/microsoft/kiota-http-go v1.5.4 // indirect
	github.com/microsoft/kiota-serialization-form-go v1.1.3 // indirect
	github.com/microsoft/kiota-serialization-json-go v1.1.2 // indirect
	github.com/microsoft/kiota-serialization-multipart-go v1.1.2 // indirect
	github.com/microsoft/kiota-serialization-text-go v1.1.3 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/rcrowley/go-metrics v0.0.0-20250401214520-65e299d6c5c9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/std-uritemplate/std-uritemplate/go/v2 v2.0.3 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/tchap/go-patricia/v2 v2.3.3 // indirect
	github.com/valyala/fastjson v1.6.7 // indirect
	github.com/vektah/gqlparser/v2 v2.5.32 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/yashtewari/glob-intersection v0.2.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.65.0 // indirect
	go.opentelemetry.io/otel v1.42.0 // indirect
	go.opentelemetry.io/otel/metric v1.42.0 // indirect
	go.opentelemetry.io/otel/trace v1.42.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260319201613-d00831a3d3e7 // indirect
	google.golang.org/grpc v1.79.3 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	modernc.org/libc v1.70.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
