package aws

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codedeploy"
	cdtypes "github.com/aws/aws-sdk-go-v2/service/codedeploy/types"
	"github.com/aws/aws-sdk-go-v2/service/databasemigrationservice"
	dmstypes "github.com/aws/aws-sdk-go-v2/service/databasemigrationservice/types"
	"github.com/aws/aws-sdk-go-v2/service/dax"
	daxtypes "github.com/aws/aws-sdk-go-v2/service/dax/types"
	"github.com/aws/aws-sdk-go-v2/service/dlm"
	dlmtypes "github.com/aws/aws-sdk-go-v2/service/dlm/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	eventstypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	gdtypes "github.com/aws/aws-sdk-go-v2/service/guardduty/types"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	istypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	ibtypes "github.com/aws/aws-sdk-go-v2/service/imagebuilder/types"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	iwtypes "github.com/aws/aws-sdk-go-v2/service/iotwireless/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	lftypes "github.com/aws/aws-sdk-go-v2/service/lakeformation/types"
	mltypes "github.com/aws/aws-sdk-go-v2/service/machinelearning/types"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	s3controltypes "github.com/aws/aws-sdk-go-v2/service/s3control/types"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"

	"github.com/icearp/disco-cli/store"
)

// TestPerServiceARNHelpers_Partition pins every per-service NativeID helper
// that takes a partition: for "aws" it returns the pre-partition commercial
// form byte for byte, and for aws-cn and aws-us-gov it changes the partition
// segment and nothing else.
func TestPerServiceARNHelpers_Partition(t *testing.T) {
	const r, a = "us-east-1", "123456789012"
	cases := []struct {
		name       string
		build      func(p string) string
		commercial string
	}{
		{"aidevops", func(p string) string { return aidevopsARN(p, r, a, "service", "s1") }, "arn:aws:aidevops:us-east-1:123456789012:service/s1"},
		{"appflow connector", func(p string) string { return appflowConnectorNativeID(p, r, a, "l1") }, "arn:aws:appflow:us-east-1:123456789012:connector/l1"},
		{"app autoscaling target", func(p string) string {
			return applicationAutoScalingScalableTargetNativeID(p, r, a, "ecs", "service/c/s", "ecs:service:DesiredCount")
		}, "arn:aws:application-autoscaling:us-east-1:123456789012:scalable-target/ecs/service/c/s/ecs:service:DesiredCount"},
		{"application insights", func(p string) string { return applicationInsightsAppNativeID(p, r, a, "rg") }, "arn:aws:applicationinsights:us-east-1:123456789012:application/resource-group/rg"},
		{"appstream", func(p string) string { return asARN(p, r, a, "directory-config", "d") }, "arn:aws:appstream:us-east-1:123456789012:directory-config/d"},
		{"arc zonal shift observer", func(p string) string { return arcZonalShiftObserverNativeID(p, r, a) }, "arn:aws:arc-zonal-shift:us-east-1:123456789012:autoshift-observer-notification-status"},
		{"autoscaling plan", func(p string) string { return autoScalingPlanARN(p, r, a, "plan", 3) }, "arn:aws:autoscaling:us-east-1:123456789012:scalingPlan/plan/3"},
		{"bcm pricing calculator", func(p string) string { return bcmPricingCalculatorNativeID(p, a, "bill-scenario", "b1") }, "arn:aws:bcm-pricing-calculator::123456789012:bill-scenario/b1"},
		{"bedrock guardrail", func(p string) string { return bedrockGuardrailARN(p, r, a, "g1") }, "arn:aws:bedrock:us-east-1:123456789012:guardrail/g1"},
		{"bedrock agentcore", func(p string) string { return bacARN(p, r, a, "gateway", "gw") }, "arn:aws:bedrock-agentcore:us-east-1:123456789012:gateway/gw"},
		{"chime sip rule", func(p string) string { return chimeSipRuleNativeID(p, r, a, "sr") }, "arn:aws:chime:us-east-1:123456789012:sip-rule/sr"},
		{"codeartifact domain", func(p string) string { return codeArtifactDomainARN(p, r, a, "dom") }, "arn:aws:codeartifact:us-east-1:123456789012:domain/dom"},
		{"codedeploy", func(p string) string { return codeDeployARN(p, r, a, "application", "app") }, "arn:aws:codedeploy:us-east-1:123456789012:application:app"},
		{"compute optimizer preference", func(p string) string {
			return computeOptimizerPreferenceNativeID(p, r, a, "Ec2Instance", "AccountId", "123456789012")
		}, "arn:aws:compute-optimizer:us-east-1:123456789012:recommendation-preferences/Ec2Instance/AccountId/123456789012"},
		{"customer profiles", func(p string) string { return cpARN(p, r, a, "dom", "layout", "l") }, "arn:aws:profile:us-east-1:123456789012:domains/dom/layout/l"},
		{"datazone", func(p string) string { return dzARN(p, r, a, "dzd", "project", "pr") }, "arn:aws:datazone:us-east-1:123456789012:domain/dzd/project/pr"},
		{"dax", func(p string) string { return daxARN(p, r, a, "subnet-group", "sg") }, "arn:aws:dax:us-east-1:123456789012:subnet-group/sg"},
		{"deadline farm", func(p string) string { return deadlineFarmARN(p, r, a, "farm-1") }, "arn:aws:deadline:us-east-1:123456789012:farm/farm-1"},
		{"deadline license endpoint", func(p string) string { return deadlineLicenseEndpointARN(p, r, a, "le-1") }, "arn:aws:deadline:us-east-1:123456789012:license-endpoint/le-1"},
		{"directory service", func(p string) string { return dsDirectoryARN(p, r, a, "d-1") }, "arn:aws:ds:us-east-1:123456789012:directory/d-1"},
		{"dms subnet group", func(p string) string { return dmsReplicationSubnetGroupARNFromName(p, r, a, "sg") }, "arn:aws:dms:us-east-1:123456789012:subgrp:sg"},
		{"ecs account setting", func(p string) string {
			return ecsAccountSettingNativeID(p, r, a, ecstypes.SettingNameContainerInsights)
		}, "arn:aws:ecs:us-east-1:123456789012:account-setting/containerInsights"},
		{"ecs capacity provider", func(p string) string { return ecsCapacityProviderARN(p, r, a, "cp") }, "arn:aws:ecs:us-east-1:123456789012:capacity-provider/cp"},
		{"beanstalk template", func(p string) string { return beanstalkTemplateARN(p, r, a, "app", "tpl") }, "arn:aws:elasticbeanstalk:us-east-1:123456789012:configurationtemplate/app/tpl"},
		{"frauddetector", func(p string) string { return fdARN(p, r, a, "label", "l") }, "arn:aws:frauddetector:us-east-1:123456789012:label/l"},
		{"fsx", func(p string) string { return fsxARN(p, r, a, "volume", "v") }, "arn:aws:fsx:us-east-1:123456789012:volume/v"},
		{"global accelerator byoip", func(p string) string { return gaByoipCidrNativeID(p, a, "10.0.0.0/24") }, "arn:aws:globalaccelerator::123456789012:byoip-cidr/10.0.0.0/24"},
		{"guardduty rule org config", func(p string) string {
			return gdRuleOrgConfigNativeID(p, r, a, "rule-1", gdtypes.AssociationMode("ALL"))
		}, "arn:aws:guardduty:us-east-1:123456789012:detection-rule/custom/rule-1/org-configuration/ALL"},
		{"healthlake profile", func(p string) string { return healthLakeProfileARN(p, r, a, "pf") }, "arn:aws:healthlake:us-east-1:123456789012:dataTransformationProfile/pf"},
		{"inspector2 delegated admin", func(p string) string { return inspector2DelegatedAdminNativeID(p, r, a, "111111111111") }, "arn:aws:inspector2:us-east-1:123456789012:delegated-admin/111111111111"},
		{"inspector2 member", func(p string) string { return inspector2MemberNativeID(p, r, a, "111111111111") }, "arn:aws:inspector2:us-east-1:123456789012:member/111111111111"},
		{"iot audit suppression", func(p string) string {
			return iotAuditSuppressionNativeID(p, r, a, iottypes.AuditSuppression{
				CheckName:          sdkaws.String("CHECK"),
				ResourceIdentifier: &iottypes.ResourceIdentifier{ClientId: sdkaws.String("c1")},
			})
		}, "arn:aws:iot:us-east-1:123456789012:audit-suppression/CHECK/" + iotResourceIdentifierKey(&iottypes.ResourceIdentifier{ClientId: sdkaws.String("c1")})},
		{"iot managed integrations", func(p string) string { return imiARN(p, r, a, "destination", "d") }, "arn:aws:iotmanagedintegrations:us-east-1:123456789012:destination/d"},
		{"iot sitewise", func(p string) string { return iotSWARN(p, r, a, "portal", "pt") }, "arn:aws:iotsitewise:us-east-1:123456789012:portal/pt"},
		{"iot wireless event config", func(p string) string {
			return iwEventConfigNativeID(p, r, a, iwtypes.EventConfigurationItem{IdentifierType: "PartnerAccountId", Identifier: sdkaws.String("pa")})
		}, "arn:aws:iotwireless:us-east-1:123456789012:" + iwEventConfigParentKinds["PartnerAccountId"] + "/pa/event-configuration"},
		{"kms custom key store", func(p string) string { return kmsCustomKeyStoreNativeID(p, r, a, "cks-1") }, "arn:aws:kms:us-east-1:123456789012:custom-key-store/cks-1"},
		{"location", func(p string) string { return locARN(p, r, a, "tracker", "t") }, "arn:aws:geo:us-east-1:123456789012:tracker/t"},
		{"machine learning", func(p string) string { return mlNativeID(p, r, a, "mlmodel", "m") }, "arn:aws:machinelearning:us-east-1:123456789012:mlmodel/m"},
		{"medialive", func(p string) string { return mlARN(p, r, a, "multiplexprogram", "mx/p") }, "arn:aws:medialive:us-east-1:123456789012:multiplexprogram/mx/p"},
		{"medialive colon", func(p string) string { return mediaLiveColonARN(p, r, a, "channel", "1") }, "arn:aws:medialive:us-east-1:123456789012:channel:1"},
		{"medialive list-or-arn", func(p string) string { return listOrARN(p, "1", r, a, "input") }, "arn:aws:medialive:us-east-1:123456789012:input:1"},
		{"mediatailor source location", func(p string) string { return mtSourceLocationARN(p, r, a, "sl") }, "arn:aws:mediatailor:us-east-1:123456789012:sourceLocation/sl"},
		{"neptune graph", func(p string) string { return neptuneGraphARN(p, r, a, "g-1") }, "arn:aws:neptune-graph:us-east-1:123456789012:graph/g-1"},
		{"opensearch", func(p string) string { return openSearchARN(p, r, a, "package/pk") }, "arn:aws:es:us-east-1:123456789012:package/pk"},
		{"opensearch connection domain", func(p string) string {
			return openSearchConnectionDomainARN(p, &opensearchtypes.DomainInformationContainer{AWSDomainInformation: &opensearchtypes.AWSDomainInformation{
				Region: sdkaws.String(r), OwnerId: sdkaws.String("111111111111"), DomainName: sdkaws.String("dom"),
			}})
		}, "arn:aws:es:us-east-1:111111111111:domain/dom"},
		{"opensearch serverless", func(p string) string { return ossARN(p, r, a, "vpce", "v") }, "arn:aws:aoss:us-east-1:123456789012:vpce/v"},
		{"pinpoint app", func(p string) string { return pinpointAppARN(p, r, a, "app") }, "arn:aws:mobiletargeting:us-east-1:123456789012:apps/app"},
		{"pinpoint child", func(p string) string { return pinpointARN(p, r, a, "segments/s", "app") }, "arn:aws:mobiletargeting:us-east-1:123456789012:apps/app/segments/s"},
		{"qapps instance", func(p string) string { return qappsInstanceARN(p, r, a, "inst") }, "arn:aws:qapps:us-east-1:123456789012:application/inst"},
		{"qapps instances", func(p string) string { return qappsInstances(p, r, a, []string{"inst"})[0].arn }, "arn:aws:qapps:us-east-1:123456789012:application/inst"},
		{"redshift", func(p string) string { return redshiftIDARN(p, r, a, "usagelimit", "u") }, "arn:aws:redshift:us-east-1:123456789012:usagelimit:u"},
		{"redshift serverless namespace", func(p string) string { return rssNamespaceARN(p, r, a, "ns") }, "arn:aws:redshift-serverless:us-east-1:123456789012:namespace/ns"},
		{"route53 resolver", func(p string) string { return r53rARN(p, r, a, "firewall-config", "f") }, "arn:aws:route53resolver:us-east-1:123456789012:firewall-config/f"},
		{"rum app monitor", func(p string) string { return rumAppMonitorARN(p, r, a, "m") }, "arn:aws:rum:us-east-1:123456789012:appmonitor/m"},
		{"s3 bucket", func(p string) string { return s3BucketARN(p, "b") }, "arn:aws:s3:::b"},
		{"supply chain instance", func(p string) string { return scnInstanceARN(p, r, a, "i") }, "arn:aws:scn:us-east-1:123456789012:instance/i"},
		{"security hub", func(p string) string { return securityHubHubNativeID(p, r, a) }, "arn:aws:securityhub:us-east-1:123456789012:hub/default"},
		{"service catalog", func(p string) string { return scARN(p, r, a, "tag-option", "t") }, "arn:aws:servicecatalog:us-east-1:123456789012:tag-option/t"},
		{"service quotas template", func(p string) string { return sqTemplateEntryNativeID(p, r, a, "ec2", "L-1") }, "arn:aws:servicequotas:us-east-1:123456789012:ec2/L-1/request-template"},
		{"ses mail manager", func(p string) string { return mmARN(p, r, a, "smtp-relay", "rl") }, "arn:aws:ses:us-east-1:123456789012:mailmanager-smtp-relay/rl"},
		{"sns origination number", func(p string) string { return snsOriginationNumberNativeID(p, r, a, "+15550100") }, "arn:aws:sns:us-east-1:123456789012:origination-number/+15550100"},
		{"textract adapter", func(p string) string { return textractAdapterNativeID(p, r, a, "ad") }, "arn:aws:textract:us-east-1:123456789012:adapter/ad"},
		{"transcribe", func(p string) string { return transcribeNativeID(p, r, a, "vocabulary", "v") }, "arn:aws:transcribe:us-east-1:123456789012:vocabulary/v"},
		{"waf classic", func(p string) string { return wafARN(p, a, "webacl", "w") }, "arn:aws:waf::123456789012:webacl/w"},
		{"waf regional", func(p string) string { return wafRegionalARN(p, r, a, "webacl", "w") }, "arn:aws:waf-regional:us-east-1:123456789012:webacl/w"},
		{"wafv2 api key", func(p string) string { return wafv2APIKeyNativeID(p, r, a, wafv2types.ScopeRegional, "k") }, "arn:aws:wafv2:us-east-1:123456789012:regional/apikey/k"},
		{"lakeformation opt-in", func(p string) string {
			id, err := lfOptInARN(p, r, a, lftypes.LakeFormationOptInsInfo{})
			if err != nil {
				return err.Error()
			}
			return id
		}, "arn:aws:lakeformation:us-east-1:123456789012:lake-formation-opt-in/44136fa355b3678a"},
		{"s3 bucket from s3 url", func(p string) string { return s3BucketARNFromS3URL(p, "s3://b/k") }, "arn:aws:s3:::b"},
		{"sso instance from permission set", func(p string) string {
			return instanceArnFromPermissionSetArn(p, "arn:"+p+":sso:::permissionSet/ssoins-1/ps-1")
		}, "arn:aws:sso:::instance/ssoins-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.build("aws"); got != c.commercial {
				t.Errorf("build(aws) = %q; want %q", got, c.commercial)
			}
			for _, pf := range partitionFixtures {
				want := "arn:" + pf.partition + strings.TrimPrefix(c.commercial, "arn:aws")
				if got := c.build(pf.partition); got != want {
					t.Errorf("build(%s) = %q; want %q", pf.partition, got, want)
				}
			}
		})
	}
}

func TestScanDLM_UsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(map[string]func(in any) (any, error){
				"GetLifecyclePolicies": func(any) (any, error) {
					return &dlm.GetLifecyclePoliciesOutput{Policies: []dlmtypes.LifecyclePolicySummary{{PolicyId: ptrStr("policy-1")}}}, nil
				},
			}, errors.New("unexpected op"))
			if _, _, err := scanDLM(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanDLM: %v", err)
			}
			want := partitionARN(pf.partition, "dlm", pf.region, "policy/policy-1")
			if rows := rowsByNativeID(t, st, TypeDLMLifecyclePolicy); len(rows) != 1 || rows[want].NativeID != want {
				t.Errorf("policy rows = %v; want one row %s", rows, want)
			}
		})
	}
}

// The key resolver rebuilds the custom key store's NativeID from the key's
// bare store id, so it must land on the row the store scanner wrote.
func TestKMSCustomKeyStore_ScannerAndResolverAgreeOnPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			client := kms.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"DescribeCustomKeyStores": {{Output: &kms.DescribeCustomKeyStoresOutput{CustomKeyStores: []kmstypes.CustomKeyStoresListEntry{kmsHSMStore()}}}},
			}), pf.region))
			if _, _, err := scanKMSCustomKeyStores(context.Background(), client, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanKMSCustomKeyStores: %v", err)
			}
			storeARN := partitionARN(pf.partition, "kms", pf.region, "custom-key-store/"+kmsHSMStoreID)
			if rows := rowsByNativeID(t, st, TypeKMSCustomKeyStore); len(rows) != 1 || rows[storeARN].NativeID != storeARN {
				t.Fatalf("store rows = %v; want one row %s", rows, storeARN)
			}
			key := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, partitionARN(pf.partition, "kms", pf.region, "key/k1"), pf.region,
				mustJSON(kmsKeyAttrs{Metadata: kmstypes.KeyMetadata{KeyId: sdkaws.String("k1"), CustomKeyStoreId: sdkaws.String(kmsHSMStoreID)}}))
			if err := resolveKMSKeyCustomKeyStores(acct, st); err != nil {
				t.Fatalf("resolveKMSKeyCustomKeyStores: %v", err)
			}
			assertEdgeTargets(t, st, key, store.ResourceID("aws", acct.ID, storeARN))
		})
	}
}

func TestResolveCloudTrail_BucketUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			bucketARN := "arn:" + pf.partition + ":s3:::audit"
			upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")
			trail := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailTrail,
				partitionARN(pf.partition, "cloudtrail", pf.region, "trail/t1"), pf.region, `{"Trail":{"S3BucketName":"audit"}}`)
			if err := resolveCloudTrailRelationships(acct, st); err != nil {
				t.Fatalf("resolveCloudTrailRelationships: %v", err)
			}
			assertEdgeTargets(t, st, trail, store.ResourceID("aws", acct.ID, bucketARN))
		})
	}
}

func TestResolveEMRClusterRefs_RoleUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			roleARN := "arn:" + pf.partition + ":iam::" + testAccountID + ":role/EMR_DefaultRole"
			role := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
			cluster := upsertTestResource(t, st, "aws", acct.ID, TypeEMRCluster,
				partitionARN(pf.partition, "elasticmapreduce", pf.region, "cluster/j-1"), pf.region, `{"ServiceRole":"EMR_DefaultRole"}`)
			if err := resolveEMRClusterRefs(acct, st); err != nil {
				t.Fatalf("resolveEMRClusterRefs: %v", err)
			}
			assertEdgeTargets(t, st, cluster, role)
		})
	}
}

func TestResolveEventBridge_DefaultBusUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			bus := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus,
				partitionARN(pf.partition, "events", pf.region, "event-bus/default"), pf.region, "{}")
			rule := upsertTestResource(t, st, "aws", acct.ID, TypeEventsRule,
				partitionARN(pf.partition, "events", pf.region, "rule/r1"), pf.region,
				eventBridgeRuleAttrs(eventstypes.Rule{EventBusName: sdkaws.String("default")}))
			if err := resolveEventBridgeRelationships(acct, st); err != nil {
				t.Fatalf("resolveEventBridgeRelationships: %v", err)
			}
			assertEdgeTargets(t, st, rule, bus)
		})
	}
}

// storedRowID fails unless typ has exactly one row and its NativeID is want,
// and returns that row's ResourceID.
func storedRowID(t *testing.T, st *store.Store, typ, want string) string {
	t.Helper()
	rows := rowsByNativeID(t, st, typ)
	row, ok := rows[want]
	if !ok || len(rows) != 1 {
		t.Fatalf("%s rows = %v; want exactly %s", typ, slices.Sorted(maps.Keys(rows)), want)
	}
	return row.ID
}

// The deployment-group resolver rebuilds the application and deployment-config
// NativeIDs from names; they must land on the rows the scanners wrote.
func TestCodeDeploy_ScannerAndResolverAgreeOnPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			client := codedeploy.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"ListApplications": {{Output: &codedeploy.ListApplicationsOutput{Applications: []string{"app"}}}},
				"BatchGetApplications": {{Output: &codedeploy.BatchGetApplicationsOutput{
					ApplicationsInfo: []cdtypes.ApplicationInfo{{ApplicationName: ptrStr("app")}},
				}}},
				"ListDeploymentConfigs": {{Output: &codedeploy.ListDeploymentConfigsOutput{DeploymentConfigsList: []string{"OneAtATime"}}}},
				"GetDeploymentConfig": {{Output: &codedeploy.GetDeploymentConfigOutput{
					DeploymentConfigInfo: &cdtypes.DeploymentConfigInfo{DeploymentConfigName: ptrStr("OneAtATime")},
				}}},
			}), pf.region))
			ctx := context.Background()
			if _, _, _, err := scanCDApplications(ctx, client, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanCDApplications: %v", err)
			}
			if _, _, err := scanCDDeploymentConfigs(ctx, client, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanCDDeploymentConfigs: %v", err)
			}
			app := storedRowID(t, st, TypeCodeDeployApplication, partitionARN(pf.partition, "codedeploy", pf.region, "application:app"))
			cfg := storedRowID(t, st, TypeCodeDeployDeploymentConfig, partitionARN(pf.partition, "codedeploy", pf.region, "deploymentconfig:OneAtATime"))
			dg := upsertTestResource(t, st, "aws", acct.ID, TypeCodeDeployDeploymentGroup,
				partitionARN(pf.partition, "codedeploy", pf.region, "deploymentgroup:app/dg"), pf.region,
				mustJSON(cdtypes.DeploymentGroupInfo{ApplicationName: ptrStr("app"), DeploymentConfigName: ptrStr("OneAtATime")}))
			if err := resolveCodeDeployDGRefs(acct, st); err != nil {
				t.Fatalf("resolveCodeDeployDGRefs: %v", err)
			}
			assertEdgeTargets(t, st, dg, app, cfg)
		})
	}
}

func TestDAX_ScannerAndResolverAgreeOnPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			client := dax.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"DescribeParameterGroups": {{Output: &dax.DescribeParameterGroupsOutput{ParameterGroups: []daxtypes.ParameterGroup{{ParameterGroupName: ptrStr("pg")}}}}},
				"DescribeSubnetGroups":    {{Output: &dax.DescribeSubnetGroupsOutput{SubnetGroups: []daxtypes.SubnetGroup{{SubnetGroupName: ptrStr("sg")}}}}},
			}), pf.region))
			ctx := context.Background()
			if _, _, err := scanDAXParameterGroups(ctx, client, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanDAXParameterGroups: %v", err)
			}
			if _, _, err := scanDAXSubnetGroups(ctx, client, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanDAXSubnetGroups: %v", err)
			}
			pg := storedRowID(t, st, TypeDAXParameterGroup, partitionARN(pf.partition, "dax", pf.region, "parameter-group/pg"))
			sg := storedRowID(t, st, TypeDAXSubnetGroup, partitionARN(pf.partition, "dax", pf.region, "subnet-group/sg"))
			cluster := upsertTestResource(t, st, "aws", acct.ID, TypeDAXCluster,
				partitionARN(pf.partition, "dax", pf.region, "cache/c1"), pf.region,
				mustJSON(daxtypes.Cluster{ParameterGroup: &daxtypes.ParameterGroupStatus{ParameterGroupName: ptrStr("pg")}, SubnetGroup: ptrStr("sg")}))
			if err := resolveDAXClusterRefs(acct, st); err != nil {
				t.Fatalf("resolveDAXClusterRefs: %v", err)
			}
			assertEdgeTargets(t, st, cluster, pg, sg)
		})
	}
}

func TestDMSSubnetGroup_ScannerAndResolverAgreeOnPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			client := databasemigrationservice.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"DescribeReplicationSubnetGroups": {{Output: &databasemigrationservice.DescribeReplicationSubnetGroupsOutput{
					ReplicationSubnetGroups: []dmstypes.ReplicationSubnetGroup{{ReplicationSubnetGroupIdentifier: ptrStr("rsg")}},
				}}},
			}), pf.region))
			if _, _, err := scanDMSReplicationSubnetGroups(context.Background(), client, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanDMSReplicationSubnetGroups: %v", err)
			}
			rsg := storedRowID(t, st, TypeDMSReplicationSubnetGroup, partitionARN(pf.partition, "dms", pf.region, "subgrp:rsg"))
			ri := upsertTestResource(t, st, "aws", acct.ID, TypeDMSReplicationInstance,
				partitionARN(pf.partition, "dms", pf.region, "rep:ri-1"), pf.region,
				mustJSON(dmstypes.ReplicationInstance{ReplicationSubnetGroup: &dmstypes.ReplicationSubnetGroup{ReplicationSubnetGroupIdentifier: ptrStr("rsg")}}))
			if err := resolveDMSReplicationInstanceRefs(acct, st); err != nil {
				t.Fatalf("resolveDMSReplicationInstanceRefs: %v", err)
			}
			assertEdgeTargets(t, st, ri, rsg)
		})
	}
}

// The scanner keys memberships in the account's partition; the resolver must
// parse that NativeID in any partition to reach the group and the user.
func TestIdentityStoreMembership_ScannerAndResolverAgreeOnPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			const storeID, owner = "d-1", "111111111111"
			groups := stubCall{Output: &identitystore.ListGroupsOutput{Groups: []istypes.Group{{GroupId: ptrStr("g-1"), IdentityStoreId: ptrStr(storeID)}}}}
			client := identitystore.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"ListUsers":  {{Output: &identitystore.ListUsersOutput{Users: []istypes.User{{UserId: ptrStr("u-1"), IdentityStoreId: ptrStr(storeID)}}}}},
				"ListGroups": {groups, groups},
				"ListGroupMemberships": {{Output: &identitystore.ListGroupMembershipsOutput{GroupMemberships: []istypes.GroupMembership{{
					MembershipId: ptrStr("m-1"), GroupId: ptrStr("g-1"), IdentityStoreId: ptrStr(storeID),
					MemberId: &istypes.MemberIdMemberUserId{Value: "u-1"},
				}}}}},
			}), pf.region))
			ctx := context.Background()
			if _, _, err := scanIdentityStoreUsers(ctx, client, acct, pf.region, storeID, owner, st, testScanID); err != nil {
				t.Fatalf("scanIdentityStoreUsers: %v", err)
			}
			if _, _, err := scanIdentityStoreGroups(ctx, client, acct, pf.region, storeID, owner, st, testScanID); err != nil {
				t.Fatalf("scanIdentityStoreGroups: %v", err)
			}
			if _, _, err := scanIdentityStoreGroupMemberships(ctx, client, acct, pf.region, storeID, owner, st, testScanID); err != nil {
				t.Fatalf("scanIdentityStoreGroupMemberships: %v", err)
			}
			idARN := func(res string) string { return "arn:" + pf.partition + ":identitystore::" + owner + ":" + res }
			user := storedRowID(t, st, TypeIdentityStoreUser, idARN("user/"+storeID+"/u-1"))
			group := storedRowID(t, st, TypeIdentityStoreGroup, idARN("group/"+storeID+"/g-1"))
			membership := storedRowID(t, st, TypeIdentityStoreGroupMembership, idARN("membership/"+storeID+"/m-1"))
			if err := resolveIdentityStoreGroupMembershipRefs(acct, st); err != nil {
				t.Fatalf("resolveIdentityStoreGroupMembershipRefs: %v", err)
			}
			assertEdgeTargets(t, st, membership, group, user)
		})
	}
}

func TestResolveS3_BucketTargetsUseAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			bucket := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:"+pf.partition+":s3:::b1", pf.region, "{}")
			keyARN := partitionARN(pf.partition, "kms", pf.region, "key/k1")
			key := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, pf.region, "{}")
			ap := upsertTestResource(t, st, "aws", acct.ID, TypeS3AccessPoint,
				partitionARN(pf.partition, "s3", pf.region, "accesspoint/ap1"), pf.region,
				mustJSON(s3controltypes.AccessPoint{Bucket: ptrStr("b1"), Name: ptrStr("ap1")}))
			acct.s3BucketEncryption = map[string]s3BucketEncryptionEntry{"b1": {Region: pf.region, Config: sseConfig("aws:kms", keyARN)}}
			if err := resolveS3BucketEncryptionRelationships(acct, st); err != nil {
				t.Fatalf("resolveS3BucketEncryptionRelationships: %v", err)
			}
			if err := resolveS3AccessPointRelationships(acct, st); err != nil {
				t.Fatalf("resolveS3AccessPointRelationships: %v", err)
			}
			assertEdgeTargets(t, st, bucket, key)
			assertEdgeTargets(t, st, ap, bucket)
		})
	}
}

func TestResolveImageBuilderInfraInstanceProfile_UsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			ip := upsertTestResource(t, st, "aws", acct.ID, TypeIAMInstanceProfile,
				"arn:"+pf.partition+":iam::"+testAccountID+":instance-profile/MyProf", "", "{}")
			icARN := partitionARN(pf.partition, "imagebuilder", pf.region, "infrastructure-configuration/inf1")
			ic := upsertTestResource(t, st, "aws", acct.ID, TypeImageBuilderInfrastructureConfig, icARN, pf.region,
				mustJSON(ibtypes.InfrastructureConfigurationSummary{Arn: &icARN, InstanceProfileName: ptrStr("MyProf")}))
			if err := resolveImageBuilderInfraInstanceProfile(acct, st); err != nil {
				t.Fatalf("resolveImageBuilderInfraInstanceProfile: %v", err)
			}
			assertEdgeTargets(t, st, ic, ip)
		})
	}
}

func TestResolveSSOPermissionSetInstance_UsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			insARN := "arn:" + pf.partition + ":sso:::instance/" + testSSOInsID
			ins := upsertTestResource(t, st, "aws", acct.ID, TypeSSOInstance, insARN, pf.region,
				`{"InstanceArn":"`+insARN+`","IdentityStoreId":"`+testIdentityID+`","OwnerAccountId":"`+testOwnerAcct+`"}`)
			ps := upsertTestResource(t, st, "aws", acct.ID, TypeSSOPermissionSet,
				"arn:"+pf.partition+":sso:::permissionSet/"+testSSOInsID+"/"+testPSId, pf.region, "{}")
			if err := resolveSSOPermissionSetInstance(acct, st); err != nil {
				t.Fatalf("resolveSSOPermissionSetInstance: %v", err)
			}
			assertEdgeTargets(t, st, ins, ps)
		})
	}
}

func TestResolveFSxSnapshotToVolume_UsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			vol := upsertTestResource(t, st, "aws", acct.ID, TypeFSxVolume,
				partitionARN(pf.partition, "fsx", pf.region, "volume/fsvol-1"), pf.region, "{}")
			snap := upsertTestResource(t, st, "aws", acct.ID, TypeFSxSnapshot,
				partitionARN(pf.partition, "fsx", pf.region, "snapshot/fsvolsnap-1"), pf.region, `{"VolumeId":"fsvol-1"}`)
			if err := resolveFSxSnapshotToVolume(acct, st); err != nil {
				t.Fatalf("resolveFSxSnapshotToVolume: %v", err)
			}
			assertEdgeTargets(t, st, snap, vol)
		})
	}
}

func TestResolveMachineLearningRefs_UsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			pipeline := upsertTestResource(t, st, "aws", acct.ID, TypeDataPipelinePipeline,
				partitionARN(pf.partition, "datapipeline", pf.region, "pipeline/df-1"), pf.region, "{}")
			ds := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningDataSource,
				partitionARN(pf.partition, "machinelearning", pf.region, "datasource/ds-1"), pf.region,
				`{"RDSMetadata":{"DataPipelineId":"df-1"}}`)
			if err := resolveMachineLearningRefs(acct, st); err != nil {
				t.Fatalf("resolveMachineLearningRefs: %v", err)
			}
			assertEdgeTargets(t, st, ds, pipeline)
		})
	}
}

// The datasource and model ids are rebuilt with mlNativeID; they must match
// the rows keyed in the account's partition.
func TestResolveMachineLearningRefs_ObjectTargetsUseAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			ds := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningDataSource,
				partitionARN(pf.partition, "machinelearning", pf.region, "datasource/ds-1"), pf.region, "{}")
			model := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningMLModel,
				partitionARN(pf.partition, "machinelearning", pf.region, "mlmodel/m-1"), pf.region, "{}")
			bp := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningBatchPrediction,
				partitionARN(pf.partition, "machinelearning", pf.region, "batchprediction/bp-1"), pf.region,
				mustJSON(mltypes.BatchPrediction{BatchPredictionDataSourceId: ptrStr("ds-1"), MLModelId: ptrStr("m-1")}))
			if err := resolveMachineLearningRefs(acct, st); err != nil {
				t.Fatalf("resolveMachineLearningRefs: %v", err)
			}
			assertEdgeTargets(t, st, bp, ds, model)
		})
	}
}

// SES receipt rules, IoT topic rules and Glue jobs name their S3 bucket; each
// resolver rebuilds the bucket ARN, which must be in the account's partition.
func TestResolveBucketActions_UseAccountPartition(t *testing.T) {
	bucketName := ptrStr("b1")
	cases := []struct {
		name    string
		typ     string
		attrs   string
		resolve func(*account, *store.Store) error
	}{
		{
			"ses receipt rule", TypeSESReceiptRule,
			mustJSON(sestypes.ReceiptRule{Name: ptrStr("r1"), Actions: []sestypes.ReceiptAction{{S3Action: &sestypes.S3Action{BucketName: bucketName}}}}),
			resolveSESReceiptRuleTargets,
		},
		{
			"iot topic rule", TypeIoTTopicRule,
			mustJSON(iot.GetTopicRuleOutput{Rule: &iottypes.TopicRule{Actions: []iottypes.Action{{S3: &iottypes.S3Action{BucketName: bucketName, Key: ptrStr("k")}}}}}),
			resolveIoTTopicRuleActionRefs,
		},
		{
			"glue job script", TypeGlueJob,
			mustJSON(gluetypes.Job{Name: ptrStr("j1"), Command: &gluetypes.JobCommand{ScriptLocation: ptrStr("s3://b1/scripts/j1.py")}}),
			resolveGlueJobRefs,
		},
	}
	for _, c := range cases {
		for _, pf := range partitionFixtures {
			t.Run(c.name+"/"+pf.partition, func(t *testing.T) {
				st := newTestStore(t)
				acct := partitionAccount(pf.partition)
				bucket := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:"+pf.partition+":s3:::b1", pf.region, "{}")
				src := upsertTestResource(t, st, "aws", acct.ID, c.typ, partitionARN(pf.partition, "test", pf.region, "src/1"), pf.region, c.attrs)
				if err := c.resolve(acct, st); err != nil {
					t.Fatalf("resolve: %v", err)
				}
				assertEdgeTargets(t, st, src, bucket)
			})
		}
	}
}

// Route 53 finds a website bucket by the name in its NativeID, which the S3
// scanner writes in the account's partition.
func TestRoute53AliasToS3Website_ScannerAndResolverAgreeOnPartition(t *testing.T) {
	const site = "www.example.com"
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			h := s3ServiceHandlers("")
			h["ListBuckets"] = func(string, any) (any, error) {
				return &s3.ListBucketsOutput{Buckets: s3TestBuckets(site)}, nil
			}
			h["GetBucketLocation"] = func(string, any) (any, error) {
				return &s3.GetBucketLocationOutput{LocationConstraint: s3types.BucketLocationConstraint(pf.region)}, nil
			}
			acct.cfg = s3StubConfig(h)
			if _, _, err := scanS3(context.Background(), acct, st, testScanID); err != nil {
				t.Fatalf("scanS3: %v", err)
			}
			bucket := storedRowID(t, st, TypeS3Bucket, "arn:"+pf.partition+":s3:::"+site)
			record := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet,
				"arn:"+pf.partition+":route53:::hostedzone/Z1/A/"+site, "",
				mustJSON(route53types.ResourceRecordSet{
					Name: ptrStr(site + "."), Type: route53types.RRTypeA,
					AliasTarget: &route53types.AliasTarget{DNSName: ptrStr("s3-website." + pf.region + ".amazonaws.com."), HostedZoneId: ptrStr("Z1")},
				}))
			if err := resolveRoute53AliasRelationships(acct, st); err != nil {
				t.Fatalf("resolveRoute53AliasRelationships: %v", err)
			}
			assertEdgeTargets(t, st, record, bucket)
		})
	}
}
