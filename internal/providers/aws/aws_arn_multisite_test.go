package aws

import (
	"context"
	"strings"
	"testing"

	codepipelinetypes "github.com/aws/aws-sdk-go-v2/service/codepipeline/types"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	cognitoidentitytypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentity/types"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	directconnecttypes "github.com/aws/aws-sdk-go-v2/service/directconnect/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/icearp/disco-cli/store"
)

// TestMultiSiteARNHelpers_Partition pins the single-id NativeID helpers the
// multi-site sweep added or gave a partition, and every cfnTypeMap entry it
// converted. Helpers that return several ids (lfOptInTargetNativeIDs,
// bedrockAgentParents) are not in the table. In us-east-1 under "aws" each
// returns the pre-partition commercial form byte for byte; in a China or
// GovCloud region it changes the partition and region and nothing else. The
// scanner and the resolvers of each shape call the same helper, so this is
// also the producer/consumer agreement for them.
func TestMultiSiteARNHelpers_Partition(t *testing.T) {
	const a = "123456789012"
	cfn := func(rtype, physID string) func(string, string) string {
		return func(p, r string) string { return cfnTypeMap[rtype].toNativeID(physID, p, r, a) }
	}
	ecsParam := func(p, r string) string { _, id := ecsSecretTarget(p, r, a, "db-pass"); return id }
	eksEntry := func(p string) eksAccessEntryRef {
		return eksAccessEntryRef{cluster: "c", principalARN: "arn:" + p + ":iam::123456789012:role/r"}
	}
	cases := []struct {
		name       string
		build      func(p, r string) string
		commercial string
	}{
		{"appconfig", func(p, r string) string { return acARN(p, r, a, "application", "app1", "experimentdefinition", "x1") }, "arn:aws:appconfig:us-east-1:123456789012:application/app1/experimentdefinition/x1"},
		{"qbusiness", func(p, r string) string { return qbARN(p, r, a, "application", "q1") }, "arn:aws:qbusiness:us-east-1:123456789012:application/q1"},
		{"app signals service", func(p, r string) string {
			return applicationSignalsServiceNativeID(p, r, a, map[string]string{"Type": "Service", "Name": "s"})
		}, "arn:aws:application-signals:us-east-1:123456789012:service/Name=s;Type=Service"},
		{"app signals grouping", func(p, r string) string { return applicationSignalsGroupingConfigurationNativeID(p, r, a) }, "arn:aws:application-signals:us-east-1:123456789012:grouping-configuration"},
		{"appstream fleet", func(p, r string) string { return appstreamFleetARN(p, r, a, "f") }, "arn:aws:appstream:us-east-1:123456789012:fleet/f"},
		{"appstream stack", func(p, r string) string { return appstreamStackARN(p, r, a, "s") }, "arn:aws:appstream:us-east-1:123456789012:stack/s"},
		{"athena capacity reservation", func(p, r string) string { return athenaCapacityReservationARN(p, r, a, "c") }, "arn:aws:athena:us-east-1:123456789012:capacity-reservation/c"},
		{"athena workgroup", func(p, r string) string { return athenaWorkGroupARN(p, r, a, "w") }, "arn:aws:athena:us-east-1:123456789012:workgroup/w"},
		{"athena data catalog", func(p, r string) string { return athenaDataCatalogARN(p, r, a, "d") }, "arn:aws:athena:us-east-1:123456789012:datacatalog/d"},
		{"autoscaling hook", func(p, r string) string { return autoScalingLifecycleHookARN(p, r, a, "g", "h") }, "arn:aws:autoscaling:us-east-1:123456789012:lifecycle-hook/g/h"},
		{"autoscaling warm pool", func(p, r string) string { return autoScalingWarmPoolARN(p, r, a, "g") }, "arn:aws:autoscaling:us-east-1:123456789012:warm-pool/g"},
		{"autoscaling notification", func(p, r string) string { return autoScalingNotificationConfigurationARN(p, r, a, "g", "t") }, "arn:aws:autoscaling:us-east-1:123456789012:notification-configuration/g/t"},
		{"autoscaling launch configuration", func(p, r string) string { return autoScalingLaunchConfigurationARN(p, r, a, "lc") }, "arn:aws:autoscaling:us-east-1:123456789012:launchConfiguration:*:launchConfigurationName/lc"},
		{"b2bi", func(p, r string) string { return b2biARN(p, r, a, "profile", "p1") }, "arn:aws:b2bi:us-east-1:123456789012:profile/p1"},
		{"bedrock agent", func(p, r string) string { return bedrockAgentARN(p, r, a, "ag") }, "arn:aws:bedrock:us-east-1:123456789012:agent/ag"},
		{"bedrock agent alias", func(p, r string) string { return bedrockAgentAliasARN(p, r, a, "ag", "al") }, "arn:aws:bedrock:us-east-1:123456789012:agent-alias/ag/al"},
		{"bedrock knowledge base", func(p, r string) string { return bedrockKBARN(p, r, a, "kb") }, "arn:aws:bedrock:us-east-1:123456789012:knowledge-base/kb"},
		{"bedrock data source", func(p, r string) string { return bedrockDataSourceARN(p, r, a, "kb", "ds") }, "arn:aws:bedrock:us-east-1:123456789012:knowledge-base/kb/data-source/ds"},
		{"cognito user pool", func(p, r string) string { return cognitoUserPoolARN(p, r, a, "up") }, "arn:aws:cognito-idp:us-east-1:123456789012:userpool/up"},
		{"cognito identity pool", func(p, r string) string { return cognitoIdentityPoolARN(p, r, a, "ip") }, "arn:aws:cognito-identity:us-east-1:123456789012:identitypool/ip"},
		{"cognito user pool child", func(p, r string) string { return cogUPARN(p, r, a, "up", "group", "g") }, "arn:aws:cognito-idp:us-east-1:123456789012:userpool/up/group/g"},
		{"codepipeline", func(p, r string) string { return codePipelineARN(p, r, a, "pl") }, "arn:aws:codepipeline:us-east-1:123456789012:pl"},
		{"codecommit repo", func(p, r string) string { return codeCommitRepoARN(p, r, a, "repo") }, "arn:aws:codecommit:us-east-1:123456789012:repo"},
		{"codecommit template", func(p, r string) string { return codeCommitTemplateNativeID(p, r, a, "t") }, "arn:aws:codecommit:us-east-1:123456789012:approval-rule-template/t"},
		{"connect account resource", func(p, r string) string { return connectAccountResourceARN(p, r, a, "phone-number", "n") }, "arn:aws:connect:us-east-1:123456789012:phone-number/n"},
		{"databrew", func(p, r string) string { return dbrARN(p, r, a, "dataset", "d") }, "arn:aws:databrew:us-east-1:123456789012:dataset/d"},
		{"directconnect connection", func(p, r string) string { return dxConnectionARN(p, r, a, "dxcon-1") }, "arn:aws:directconnect:us-east-1:123456789012:dxcon/dxcon-1"},
		{"directconnect interconnect", func(p, r string) string { return dxInterconnectARN(p, r, a, "dxcon-2") }, "arn:aws:directconnect:us-east-1:123456789012:dxcon/dxcon-2"},
		{"directconnect lag", func(p, r string) string { return dxLagARN(p, r, a, "dxlag-1") }, "arn:aws:directconnect:us-east-1:123456789012:dxlag/dxlag-1"},
		{"directconnect gateway", func(p, r string) string { return dxGatewayARN(p, a, "gw") }, "arn:aws:directconnect::123456789012:dx-gateway/gw"},
		{"ecr singleton", func(p, r string) string { return ecrSingletonARN(p, r, a, "registry-policy") }, "arn:aws:ecr:us-east-1:123456789012:registry-policy"},
		{"eks child", func(p, r string) string { return eksChildARN(p, r, a, "addon", "c", "k") }, "arn:aws:eks:us-east-1:123456789012:addon/c/k"},
		{"eks associated access policy", func(p, r string) string {
			return eksAssociatedAccessPolicyNativeID(p, r, a, eksEntry(p), "arn:"+p+":eks::aws:cluster-access-policy/Admin")
		}, "arn:aws:eks:us-east-1:123456789012:associated-access-policy/c/arn:aws:iam::123456789012:role/r/arn:aws:eks::aws:cluster-access-policy/Admin"},
		{"eks cluster", func(p, r string) string { return eksClusterARN(p, r, a, "c") }, "arn:aws:eks:us-east-1:123456789012:cluster/c"},
		{"emr", func(p, r string) string { return emrARN(p, r, a, "studio", "s") }, "arn:aws:elasticmapreduce:us-east-1:123456789012:studio/s"},
		{"efs file system", func(p, r string) string { return efsFileSystemARN(p, r, a, "fs-1") }, "arn:aws:elasticfilesystem:us-east-1:123456789012:file-system/fs-1"},
		{"fms resource set", func(p, r string) string { return fmsResourceSetARN(p, r, a, "rs") }, "arn:aws:fms:us-east-1:123456789012:resource-set/rs"},
		{"fms notification channel", func(p, r string) string { return fmsNotificationChannelNativeID(p, r, a) }, "arn:aws:fms:us-east-1:123456789012:notification-channel"},
		{"gamelift fleet", func(p, r string) string { return gameliftFleetARN(p, r, a, "f") }, "arn:aws:gamelift:us-east-1:123456789012:fleet/f"},
		{"glue resource", func(p, r string) string { return glueResourceARN(p, r, a, "workflow", "w") }, "arn:aws:glue:us-east-1:123456789012:workflow/w"},
		{"glue database", func(p, r string) string { return glueDatabaseARN(p, r, a, "db") }, "arn:aws:glue:us-east-1:123456789012:database/db"},
		{"glue table", func(p, r string) string { return glueTableARN(p, r, a, "db", "t") }, "arn:aws:glue:us-east-1:123456789012:table/db/t"},
		{"glue partition", func(p, r string) string { return gluePartitionARN(p, r, a, "db", "t", []string{"2024", "a/b"}) }, "arn:aws:glue:us-east-1:123456789012:partition/db/t/2024/a%2Fb"},
		{"glue table optimizer", func(p, r string) string { return glueTableOptimizerARN(p, r, a, "db", "t", "compaction") }, "arn:aws:glue:us-east-1:123456789012:table-optimizer/db/t/compaction"},
		{"glue role from name", func(p, r string) string { return glueRoleARN(p, a, "GlueRole") }, "arn:aws:iam::123456789012:role/GlueRole"},
		{"guardduty detector", func(p, r string) string { return guardDutyDetectorARN(p, r, a, "d") }, "arn:aws:guardduty:us-east-1:123456789012:detector/d"},
		{"iam account", func(p, r string) string { return iamAccountNativeID(p, a) }, "arn:aws:iam::123456789012:root"},
		{"iam delegation request", func(p, r string) string { return iamDelegationRequestNativeID(p, a, "dr") }, "arn:aws:iam::123456789012:delegation-request/dr"},
		{"iam role", func(p, r string) string { return iamRoleARN(p, a, "r") }, "arn:aws:iam::123456789012:role/r"},
		{"iam instance profile", func(p, r string) string { return iamInstanceProfileARN(p, a, "ip") }, "arn:aws:iam::123456789012:instance-profile/ip"},
		{"iot", func(p, r string) string { return iotARN(p, r, a, "thing", "t") }, "arn:aws:iot:us-east-1:123456789012:thing/t"},
		{"step functions from name", func(p, r string) string { return stateMachineARNFromName(p, r, a, "sm") }, "arn:aws:states:us-east-1:123456789012:stateMachine:sm"},
		{"kinesis from name", func(p, r string) string { return kinesisStreamARNFromName(p, r, a, "k") }, "arn:aws:kinesis:us-east-1:123456789012:stream/k"},
		{"firehose from name", func(p, r string) string { return firehoseDeliveryStreamARNFromName(p, r, a, "f") }, "arn:aws:firehose:us-east-1:123456789012:deliverystream/f"},
		{"kendra child", func(p, r string) string { return kendraChildNativeID(p, r, a, "i", "faq", "f") }, "arn:aws:kendra:us-east-1:123456789012:index/i/faq/f"},
		{"kms key", func(p, r string) string { return kmsKeyARN(p, r, a, "k1") }, "arn:aws:kms:us-east-1:123456789012:key/k1"},
		{"kms key from bare id", func(p, r string) string { return kmsKeyRefARN(p, r, a, "k1") }, "arn:aws:kms:us-east-1:123456789012:key/k1"},
		{"kms key from its arn", func(p, r string) string { return kmsKeyRefARN(p, r, a, kmsKeyARN(p, r, a, "k1")) }, "arn:aws:kms:us-east-1:123456789012:key/k1"},
		{"event bus", func(p, r string) string { return eventBusARN(p, r, a, "b") }, "arn:aws:events:us-east-1:123456789012:event-bus/b"},
		{"lakeformation data cells filter", func(p, r string) string { return lfDataCellsFilterARN(p, r, a, "db", "t", "f") }, "arn:aws:lakeformation:us-east-1:123456789012:data-cells-filter/db/t/f"},
		{"lakeformation lf-tag", func(p, r string) string { return lfTagARN(p, r, a, "k") }, "arn:aws:lakeformation:us-east-1:123456789012:lf-tag/k"},
		{"lex bot version", func(p, r string) string { return lexBotVersionNativeID(p, r, a, "b", "1") }, "arn:aws:lex:us-east-1:123456789012:bot/b/version/1"},
		{"lex bot locale", func(p, r string) string { return lexBotLocaleNativeID(p, r, a, "b", "en_US") }, "arn:aws:lex:us-east-1:123456789012:bot/b/version/DRAFT/locale/en_US"},
		{"networkmanager global network", func(p, r string) string { return nmGlobalNetworkID(p, a, "gn") }, "arn:aws:networkmanager::123456789012:global-network/gn"},
		{"networkmanager site", func(p, r string) string { return nmSiteID(p, a, "gn", "s") }, "arn:aws:networkmanager::123456789012:site/gn/s"},
		{"networkmanager device", func(p, r string) string { return nmDeviceID(p, a, "gn", "d") }, "arn:aws:networkmanager::123456789012:device/gn/d"},
		{"networkmanager link", func(p, r string) string { return nmLinkID(p, a, "gn", "l") }, "arn:aws:networkmanager::123456789012:link/gn/l"},
		{"networkmanager core network", func(p, r string) string { return nmCoreNetworkID(p, a, "cn") }, "arn:aws:networkmanager::123456789012:core-network/cn"},
		{"redshift cluster", func(p, r string) string { return redshiftClusterARN(p, r, a, "c") }, "arn:aws:redshift:us-east-1:123456789012:cluster:c"},
		{"redshift subnet group", func(p, r string) string { return redshiftSubnetGroupARN(p, r, a, "g") }, "arn:aws:redshift:us-east-1:123456789012:subnetgroup:g"},
		{"route53", func(p, r string) string { return route53ARN(p, "hostedzone/Z1") }, "arn:aws:route53:::hostedzone/Z1"},
		{"rtbfabric link", func(p, r string) string { return rtbLinkARN(p, r, a, "g", "l") }, "arn:aws:rtbfabric:us-east-1:123456789012:gateway/g/link/l"},
		{"s3files", func(p, r string) string { return s3filesFSARN(p, r, a, "fs") }, "arn:aws:s3files:us-east-1:123456789012:file-system/fs"},
		{"sagemaker", func(p, r string) string { return sagemakerARN(p, r, a, "domain", "d") }, "arn:aws:sagemaker:us-east-1:123456789012:domain/d"},
		{"sagemaker endpoint config", func(p, r string) string { return sagemakerEndpointConfigARN(p, r, a, "e") }, "arn:aws:sagemaker:us-east-1:123456789012:endpoint-config/e"},
		{"sagemaker model", func(p, r string) string { return sagemakerModelARN(p, r, a, "m") }, "arn:aws:sagemaker:us-east-1:123456789012:model/m"},
		{"security agent space", func(p, r string) string { return saAgentSpaceARN(p, r, a, "s") }, "arn:aws:securityagent:us-east-1:123456789012:agent-space/s"},
		{"cloud map namespace", func(p, r string) string { return sdNamespaceARN(p, r, a, "ns") }, "arn:aws:servicediscovery:us-east-1:123456789012:namespace/ns"},
		{"ses", func(p, r string) string { return sesARN(p, r, a, "receipt-filter", "f") }, "arn:aws:ses:us-east-1:123456789012:receipt-filter/f"},
		{"ses receipt rule set", func(p, r string) string { return sesReceiptRuleSetARN(p, r, a, "rs") }, "arn:aws:ses:us-east-1:123456789012:receipt-rule-set/rs"},
		{"ses email identity", func(p, r string) string { return sesEmailIdentityARN(p, r, a, "x.com") }, "arn:aws:ses:us-east-1:123456789012:identity/x.com"},
		{"ses configuration set", func(p, r string) string { return sesConfigurationSetARN(p, r, a, "cs") }, "arn:aws:ses:us-east-1:123456789012:configuration-set/cs"},
		{"snowball address", func(p, r string) string { return snowballAddressNativeID(p, a, "ad") }, "arn:aws:snowball::123456789012:address/ad"},
		{"snowball cluster", func(p, r string) string { return snowballClusterNativeID(p, r, a, "c") }, "arn:aws:snowball:us-east-1:123456789012:cluster/c"},
		{"ssm parameter", func(p, r string) string { return ssmParameterARN(p, r, a, "/app/db") }, "arn:aws:ssm:us-east-1:123456789012:parameter/app/db"},
		{"ssm parameter without slash", func(p, r string) string { return ssmParameterARN(p, r, a, "plain") }, "arn:aws:ssm:us-east-1:123456789012:parameter/plain"},
		{"ssm maintenance window", func(p, r string) string { return ssmMaintenanceWindowARN(p, r, a, "mw-1") }, "arn:aws:ssm:us-east-1:123456789012:maintenancewindow/mw-1"},
		{"ecs secret from bare parameter", ecsParam, "arn:aws:ssm:us-east-1:123456789012:parameter/db-pass"},
		{"workspaces account link", func(p, r string) string { return wsAccountLinkNativeID(p, r, a, "l") }, "arn:aws:workspaces:us-east-1:123456789012:accountlink/l"},
		{"xray indexing rule", func(p, r string) string { return xrayIndexingRuleNativeID(p, r, a, "Default") }, "arn:aws:xray:us-east-1:123456789012:indexing-rule/Default"},
		{"cfn iam role", cfn("AWS::IAM::Role", "r"), "arn:aws:iam::123456789012:role/r"},
		{"cfn iam user", cfn("AWS::IAM::User", "u"), "arn:aws:iam::123456789012:user/u"},
		{"cfn lambda", cfn("AWS::Lambda::Function", "fn"), "arn:aws:lambda:us-east-1:123456789012:function:fn"},
		{"cfn dynamodb", cfn("AWS::DynamoDB::Table", "t"), "arn:aws:dynamodb:us-east-1:123456789012:table/t"},
		{"cfn sqs", func(p, r string) string {
			return cfnTypeMap["AWS::SQS::Queue"].toNativeID(testSQSQueueURL(t, p, r, "q"), p, r, a)
		}, "arn:aws:sqs:us-east-1:123456789012:q"},
		{"cfn kms", cfn("AWS::KMS::Key", "k"), "arn:aws:kms:us-east-1:123456789012:key/k"},
		{"cfn events rule", cfn("AWS::Events::Rule", "ru"), "arn:aws:events:us-east-1:123456789012:rule/ru"},
		{"cfn event bus", cfn("AWS::Events::EventBus", "b"), "arn:aws:events:us-east-1:123456789012:event-bus/b"},
		{"cfn efs", cfn("AWS::EFS::FileSystem", "fs-1"), "arn:aws:elasticfilesystem:us-east-1:123456789012:file-system/fs-1"},
		{"cfn ecr", cfn("AWS::ECR::Repository", "repo"), "arn:aws:ecr:us-east-1:123456789012:repository/repo"},
		{"cfn kinesis", cfn("AWS::Kinesis::Stream", "s"), "arn:aws:kinesis:us-east-1:123456789012:stream/s"},
		{"cfn ssm parameter", cfn("AWS::SSM::Parameter", "/p"), "arn:aws:ssm:us-east-1:123456789012:parameter/p"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.build("aws", "us-east-1"); got != c.commercial {
				t.Errorf("build(aws, us-east-1) = %q; want %q", got, c.commercial)
			}
			for _, pf := range partitionFixtures {
				want := strings.NewReplacer("arn:aws:", "arn:"+pf.partition+":", ":us-east-1:", ":"+pf.region+":").Replace(c.commercial)
				if got := c.build(pf.partition, pf.region); got != want {
					t.Errorf("build(%s, %s) = %q; want %q", pf.partition, pf.region, got, want)
				}
			}
		})
	}
}

// The identity pool resolver rebuilds the user pool and app client NativeIDs
// from the provider name; they must land on the rows the pool scanner wrote.
func TestCognitoIdentityPool_ResolverFindsScannedUserPool(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			suffix, ok := dnsSuffix(pf.partition)
			if !ok {
				t.Fatalf("dnsSuffix(%s) unknown", pf.partition)
			}
			pool := cognitoUserPoolRow(acct, pf.region, testScanID, cognitoidptypes.UserPoolDescriptionType{Id: ptrStr("up-1"), Name: ptrStr("pool")}, nil)
			client := &store.Resource{
				Provider: "aws", AccountID: acct.ID, Type: TypeCognitoAppClient, Region: &pf.region, AttributesJSON: "{}", DiscoveredBy: testScanID,
				NativeID: cogUPARN(acct.partition(), pf.region, acct.ID, "up-1", "client", "c-1"),
			}
			idPool := cognitoIdentityPoolRow(acct, pf.region, testScanID,
				cognitoidentitytypes.IdentityPoolShortDescription{IdentityPoolId: ptrStr("ip-1")},
				cognitoIDPoolRead{described: &cognitoidentity.DescribeIdentityPoolOutput{
					IdentityPoolId: ptrStr("ip-1"),
					CognitoIdentityProviders: []cognitoidentitytypes.CognitoIdentityProvider{{
						ClientId: ptrStr("c-1"), ProviderName: ptrStr("cognito-idp." + pf.region + "." + suffix + "/up-1"),
					}},
				}})
			if _, err := st.UpsertResources([]*store.Resource{pool, client, idPool}); err != nil {
				t.Fatalf("UpsertResources: %v", err)
			}
			if err := resolveCognitoIdentityPoolRelationships(acct, st); err != nil {
				t.Fatalf("resolveCognitoIdentityPoolRelationships: %v", err)
			}
			if want := partitionARN(pf.partition, "cognito-idp", pf.region, "userpool/up-1"); pool.NativeID != want {
				t.Errorf("user pool NativeID = %s; want %s", pool.NativeID, want)
			}
			assertEdgeTargets(t, st, idPool.ID, pool.ID, client.ID)
		})
	}
}

// Route 53 rows carry the synthetic route53ARN; the resolvers rebuild zones
// and traffic policies from ids and parse DNSSEC and KSK NativeIDs back to
// their parents, all of which must keep the account's partition.
func TestRoute53_ResolversKeepAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			zoneARN := route53ARN(acct.partition(), "hostedzone/Z1")
			if want := "arn:" + pf.partition + ":route53:::hostedzone/Z1"; zoneARN != want {
				t.Fatalf("route53ARN = %s; want %s", zoneARN, want)
			}
			zone := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HostedZone, zoneARN, "", "{}")
			policy := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53TrafficPolicy, route53ARN(acct.partition(), "trafficpolicy/tp-1"), "", "{}")
			instance := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53TrafficPolicyInstance, route53ARN(acct.partition(), "trafficpolicyinstance/i-1"), "",
				mustJSON(route53types.TrafficPolicyInstance{HostedZoneId: ptrStr("Z1"), TrafficPolicyId: ptrStr("tp-1")}))
			dnssec := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53DNSSEC, zoneARN+"/dnssec", "", "{}")
			ksk := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53KeySigningKey, zoneARN+"/ksk/k1", "", "{}")
			for _, resolve := range []func(*account, *store.Store) error{
				resolveRoute53TrafficPolicyInstance, resolveRoute53DNSSECRelationships, resolveRoute53KSKRelationships,
			} {
				if err := resolve(acct, st); err != nil {
					t.Fatalf("resolve: %v", err)
				}
			}
			assertEdgeTargets(t, st, instance, zone, policy)
			assertEdgeTargets(t, st, dnssec, zone)
			assertEdgeTargets(t, st, ksk, dnssec)
		})
	}
}

func TestRoute53ZoneParsers_AnyPartition(t *testing.T) {
	for _, p := range []string{"aws", "aws-cn", "aws-us-gov"} {
		zone := "arn:" + p + ":route53:::hostedzone/Z9"
		if got := recordSetZoneARN(zone + "/A/www.example.com"); got != zone {
			t.Errorf("recordSetZoneARN(%s record) = %q; want %q", p, got, zone)
		}
		if got := dnssecZoneARN(zone + "/dnssec"); got != zone {
			t.Errorf("dnssecZoneARN(%s) = %q; want %q", p, got, zone)
		}
		if got := kskDNSSECNativeID(zone + "/ksk/k"); got != zone+"/dnssec" {
			t.Errorf("kskDNSSECNativeID(%s) = %q; want %q", p, got, zone+"/dnssec")
		}
	}
	for _, bad := range []string{
		"arn:aws-cn:route53:us-east-1::hostedzone/Z9/dnssec",
		"arn:aws-cn:route53:::healthcheck/Z9/dnssec",
		"arn:aws-cn:route53:::hostedzone//dnssec",
		"arn:aws-cn:s3:::hostedzone/Z9/dnssec",
		"hostedzone/Z9/dnssec",
	} {
		if got := dnssecZoneARN(bad); got != "" {
			t.Errorf("dnssecZoneARN(%q) = %q; want empty", bad, got)
		}
	}
}

// The receipt-rule resolver rebuilds the rule set from the rule's NativeID,
// the window-target resolver the window from its WindowId, the glue table
// resolver the database from its name, the webhook resolver the pipeline from
// its name, and the LAG resolver the LAG from its id. Each target is keyed by
// the helper its scanner uses.
func TestNameRebuiltTargets_UseAccountPartition(t *testing.T) {
	cases := []struct {
		name      string
		srcType   string
		src       func(p, region string) string
		attrs     string
		tgtType   string
		tgt       func(p, region string) string
		resolve   func(*account, *store.Store) error
		tgtRegion bool
	}{
		{
			"ses receipt rule → rule set", TypeSESReceiptRule,
			func(p, r string) string { return sesReceiptRuleSetARN(p, r, testAccountID, "rs") + "/receipt-rule/r1" },
			mustJSON(sestypes.ReceiptRule{Name: ptrStr("r1")}),
			TypeSESReceiptRuleSet, func(p, r string) string { return sesReceiptRuleSetARN(p, r, testAccountID, "rs") },
			resolveSESReceiptRuleTargets, true,
		},
		{
			"ssm window target → window", TypeSSMMaintenanceWindowTarget,
			func(p, r string) string { return arnFor(p, "ssm", r, testAccountID, "windowtarget/mw-1/wt-1") },
			mustJSON(ssmtypes.MaintenanceWindowTarget{WindowId: ptrStr("mw-1")}),
			TypeSSMMaintenanceWindow, func(p, r string) string { return ssmMaintenanceWindowARN(p, r, testAccountID, "mw-1") },
			resolveSSMMaintenanceWindowTargetParent, true,
		},
		{
			"glue table → database", TypeGlueTable,
			func(p, r string) string { return glueTableARN(p, r, testAccountID, "sales", "orders") },
			mustJSON(gluetypes.Table{Name: ptrStr("orders"), DatabaseName: ptrStr("sales")}),
			TypeGlueDatabase, func(p, r string) string { return glueDatabaseARN(p, r, testAccountID, "sales") },
			resolveGlueTableDatabase, true,
		},
		{
			"codepipeline webhook → pipeline", TypeCodePipelineWebhook,
			func(p, r string) string { return codePipelineARN(p, r, testAccountID, "webhook:wh1") },
			mustJSON(codepipelinetypes.ListWebhookItem{Definition: &codepipelinetypes.WebhookDefinition{TargetPipeline: ptrStr("p1")}}),
			TypeCodePipelinePipeline, func(p, r string) string { return codePipelineARN(p, r, testAccountID, "p1") },
			resolveCodePipelineWebhookToPipeline, true,
		},
		{
			"directconnect connection → lag", TypeDirectConnectConnection,
			func(p, r string) string { return dxConnectionARN(p, r, testAccountID, "dxcon-1") },
			mustJSON(directconnecttypes.Connection{ConnectionId: ptrStr("dxcon-1"), LagId: ptrStr("dxlag-1")}),
			TypeDirectConnectLag, func(p, r string) string { return dxLagARN(p, r, testAccountID, "dxlag-1") },
			resolveDXLagMembers, true,
		},
	}
	for _, c := range cases {
		for _, pf := range partitionFixtures {
			t.Run(c.name+"/"+pf.partition, func(t *testing.T) {
				st := newTestStore(t)
				acct := partitionAccount(pf.partition)
				tgt := upsertTestResource(t, st, "aws", acct.ID, c.tgtType, c.tgt(pf.partition, pf.region), pf.region, "{}")
				src := upsertTestResource(t, st, "aws", acct.ID, c.srcType, c.src(pf.partition, pf.region), pf.region, c.attrs)
				if err := c.resolve(acct, st); err != nil {
					t.Fatalf("resolve: %v", err)
				}
				assertEdgeTargets(t, st, src, tgt)
			})
		}
	}
}

// A foreign volume owner's placeholder must share identity with the row
// scanIAMAccount writes when that account is scanned, in every partition.
func TestIAMAccount_ScannerAndOwnerResolverAgreeOnPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			owner := newTestAccount(iamTestPartnerAccount)
			owner.Partition = pf.partition
			var errs phaseErrs
			scanIAMAccount(context.Background(), stubIAMAccountAPI{summary: map[string]int32{"Users": 1}}, owner, st, testScanID, errs.add)
			if err := errs.join(st, owner, "global"); err != nil {
				t.Fatalf("scanIAMAccount: %v", err)
			}
			ownerRow := store.ResourceID("aws", iamTestPartnerAccount, "arn:"+pf.partition+":iam::"+iamTestPartnerAccount+":root")
			if _, err := st.GetResource(ownerRow); err != nil {
				t.Fatalf("scanned owner row %s: %v", ownerRow, err)
			}
			acct := partitionAccount(pf.partition)
			vol := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Volume, ec2ARNIn(acct.partition(), pf.region, acct.ID, "volume", "vol-1"), pf.region,
				mustJSON(ec2types.Volume{VolumeId: ptrStr("vol-1"), OwnerId: ptrStr(iamTestPartnerAccount)}))
			if err := resolveEC2VolumeOwners(acct, st); err != nil {
				t.Fatalf("resolveEC2VolumeOwners: %v", err)
			}
			assertEdgeTargets(t, st, vol, ownerRow)
			if rows := iamTestAccountRows(t, st); len(rows) != 1 {
				t.Errorf("account rows = %+v; want only the scanned owner, no placeholder beside it", rows)
			}
		})
	}
}

// The stack resolver turns a CloudFormation physical id into the NativeID the
// target's scanner stores; the targets here carry the ARNs AWS returns.
func TestResolveCloudFormationStackResources_UsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			iam := func(res string) string { return "arn:" + pf.partition + ":iam::" + testAccountID + ":" + res }
			targets := []struct{ rtype, physID, typ, nativeID string }{
				{"AWS::IAM::Role", "role1", TypeIAMRole, iam("role/role1")},
				{"AWS::IAM::User", "user1", TypeIAMUser, iam("user/user1")},
				{"AWS::Lambda::Function", "fn", TypeLambdaFunction, partitionARN(pf.partition, "lambda", pf.region, "function:fn")},
				{"AWS::DynamoDB::Table", "t", TypeDynamoDBTable, partitionARN(pf.partition, "dynamodb", pf.region, "table/t")},
				{"AWS::SQS::Queue", testSQSQueueURL(t, pf.partition, pf.region, "q"), TypeSQSQueue, partitionARN(pf.partition, "sqs", pf.region, "q")},
				{"AWS::KMS::Key", "k1", TypeKMSKey, partitionARN(pf.partition, "kms", pf.region, "key/k1")},
				{"AWS::Events::Rule", "ru", TypeEventsRule, partitionARN(pf.partition, "events", pf.region, "rule/ru")},
				{"AWS::Events::EventBus", "bus", TypeEventsEventBus, partitionARN(pf.partition, "events", pf.region, "event-bus/bus")},
				{"AWS::EFS::FileSystem", "fs-1", TypeEFSFileSystem, partitionARN(pf.partition, "elasticfilesystem", pf.region, "file-system/fs-1")},
				{"AWS::ECR::Repository", "repo", TypeECRRepository, partitionARN(pf.partition, "ecr", pf.region, "repository/repo")},
				{"AWS::Kinesis::Stream", "s", TypeKinesisStream, partitionARN(pf.partition, "kinesis", pf.region, "stream/s")},
				{"AWS::SSM::Parameter", "/app/p", TypeSSMParameter, partitionARN(pf.partition, "ssm", pf.region, "parameter/app/p")},
			}
			var resources []map[string]string
			var want []string
			for _, tg := range targets {
				want = append(want, upsertTestResource(t, st, "aws", acct.ID, tg.typ, tg.nativeID, pf.region, "{}"))
				resources = append(resources, map[string]string{
					"LogicalResourceId": "L", "PhysicalResourceId": tg.physID, "ResourceType": tg.rtype, "ResourceStatus": "CREATE_COMPLETE",
				})
			}
			stack := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack,
				partitionARN(pf.partition, "cloudformation", pf.region, "stack/demo/1"), pf.region, mustJSON(map[string]any{"Resources": resources}))
			if err := resolveCloudFormationStackResources(acct, st); err != nil {
				t.Fatalf("resolveCloudFormationStackResources: %v", err)
			}
			assertEdgeTargets(t, st, stack, want...)
		})
	}
}

// The alias resolver and the shared KMS index rebuild a key ARN from a bare
// key id; both must land on the ARN AWS returned for the key in the account's
// partition, whether a topic names the key by id or by alias.
func TestKMSKeyRebuiltFromBareID_UsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			key := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, partitionARN(pf.partition, "kms", pf.region, "key/k1"), pf.region, "{}")
			aliasARN := partitionARN(pf.partition, "kms", pf.region, "alias/app")
			alias := upsertTestResource(t, st, "aws", acct.ID, TypeKMSAlias, aliasARN, pf.region,
				mustJSON(kmstypes.AliasListEntry{AliasName: ptrStr("alias/app"), AliasArn: ptrStr(aliasARN), TargetKeyId: ptrStr("k1")}))
			byID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, partitionARN(pf.partition, "sns", pf.region, "by-id"), pf.region,
				mustJSON(map[string]string{"KmsMasterKeyId": "k1"}))
			byAlias := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, partitionARN(pf.partition, "sns", pf.region, "by-alias"), pf.region,
				mustJSON(map[string]string{"KmsMasterKeyId": "alias/app"}))
			if err := resolveKMSAliases(acct, st); err != nil {
				t.Fatalf("resolveKMSAliases: %v", err)
			}
			if err := resolveSNSTopicRelationships(acct, st); err != nil {
				t.Fatalf("resolveSNSTopicRelationships: %v", err)
			}
			assertEdgeTargets(t, st, alias, key)
			assertEdgeTargets(t, st, byID, key)
			assertEdgeTargets(t, st, byAlias, key)
		})
	}
}

// testSQSQueueURL is the queue URL SQS issues in partition p and region r for
// a queue of testAccountID.
func testSQSQueueURL(t *testing.T, p, r, name string) string {
	t.Helper()
	suffix, ok := dnsSuffix(p)
	if !ok {
		t.Fatalf("dnsSuffix(%s) unknown", p)
	}
	return "https://sqs." + r + "." + suffix + "/" + testAccountID + "/" + name
}
