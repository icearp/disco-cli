package aws

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/icearp/disco-cli/store"
)

// edgeTarget is a row a matcher case seeds and expects one edge to.
type edgeTarget struct{ typ, nativeID string }

// arnMatcherCase drives one resolver whose source attributes carry an ARN or
// an AWS DNS name: attrs, targets and decoys are built for partition p and its
// region r, targets and decoys are seeded, and the source row must have edges
// to exactly the targets (none when targets is empty). A decoy is the row a
// wrong parse would name, so an edge to it fails even in an FK-safe resolver.
type arnMatcherCase struct {
	name    string
	resolve func(*account, *store.Store) error
	srcType string
	attrs   func(p, r string) string
	targets func(p, r string) []edgeTarget
	decoys  func(p, r string) []edgeTarget
}

// matcherPartitions is the commercial partition plus partitionFixtures: a
// matcher must keep matching commercial ARNs while it learns the others.
var matcherPartitions = append([]struct{ partition, region string }{{"aws", "us-east-1"}}, partitionFixtures...)

// runARNMatcherCases runs each case in every matcherPartitions entry.
func runARNMatcherCases(t *testing.T, cases []arnMatcherCase) {
	t.Helper()
	for _, c := range cases {
		for _, pf := range matcherPartitions {
			t.Run(c.name+"/"+pf.partition, func(t *testing.T) {
				st := newTestStore(t)
				acct := partitionAccount(pf.partition)
				var want []string
				for _, tg := range c.targets(pf.partition, pf.region) {
					want = append(want, upsertTestResource(t, st, "aws", acct.ID, tg.typ, tg.nativeID, pf.region, "{}"))
				}
				if c.decoys != nil {
					for _, d := range c.decoys(pf.partition, pf.region) {
						upsertTestResource(t, st, "aws", acct.ID, d.typ, d.nativeID, pf.region, "{}")
					}
				}
				src := upsertTestResource(t, st, "aws", acct.ID, c.srcType,
					partitionARN(pf.partition, "disco-test", pf.region, "source/"+c.name), pf.region, c.attrs(pf.partition, pf.region))
				if err := c.resolve(acct, st); err != nil {
					t.Fatalf("resolve: %v", err)
				}
				assertEdgeTargets(t, st, src, want...)
			})
		}
	}
}

// foreignRegion is a region outside partition p. Tests build a decoy host from
// it with p's own DNS suffix, so only the region rule can reject the host.
func foreignRegion(p string) string {
	if p == "aws" {
		return "us-gov-west-1"
	}
	return "us-east-1"
}

// globalARN builds arn:{p}:{service}::{testAccountID}:{resource}.
func globalARN(p, service, resource string) string {
	return "arn:" + p + ":" + service + "::" + testAccountID + ":" + resource
}

func one(typ, nativeID string) []edgeTarget { return []edgeTarget{{typ, nativeID}} }

// TestARNPrefixMatchers_EveryPartition drives every resolver that recognises a
// target by the service of an ARN taken from an SDK response with that ARN in
// aws, aws-cn and aws-us-gov. A matcher that tests for the "arn:aws:" prefix
// emits no edge outside aws.
func TestARNPrefixMatchers_EveryPartition(t *testing.T) {
	cases := []arnMatcherCase{
		{
			name: "aiops→sns", resolve: resolveAIOpsRelationships, srcType: TypeAIOpsInvestigationGroup,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"ChatbotNotificationChannel":{%q:[]}}`, partitionARN(p, "sns", r, "alerts"))
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSNSTopic, partitionARN(p, "sns", r, "alerts")) },
		},
		{
			name: "apigw domain→acm", resolve: resolveAPIGatewayDomainCertRelationships, srcType: TypeAPIGatewayDomainName,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"RegionalCertificateArn":%q}`, partitionARN(p, "acm", r, "certificate/c1"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeACMCertificate, partitionARN(p, "acm", r, "certificate/c1"))
			},
		},
		{
			name: "apigwv2 domain→acm", resolve: resolveAPIGatewayDomainCertRelationships, srcType: TypeAPIGatewayDomainNameV2,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DomainNameConfigurations":[{"CertificateArn":%q}]}`, partitionARN(p, "acm", r, "certificate/c2"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeACMCertificate, partitionARN(p, "acm", r, "certificate/c2"))
			},
		},
		{
			name: "elbv2 listener→acm", resolve: resolveELBv2ListenerRelationships, srcType: TypeELBv2Listener,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"Certificates":[{"CertificateArn":%q}]}`, partitionARN(p, "acm", r, "certificate/c3"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeACMCertificate, partitionARN(p, "acm", r, "certificate/c3"))
			},
		},
		{
			name: "lifecycle hook→sns", resolve: resolveAutoScalingRelationships, srcType: TypeAutoScalingLifecycleHook,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"NotificationTargetARN":%q}`, partitionARN(p, "sns", r, "hooks"))
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSNSTopic, partitionARN(p, "sns", r, "hooks")) },
		},
		{
			name: "lifecycle hook→sqs", resolve: resolveAutoScalingRelationships, srcType: TypeAutoScalingLifecycleHook,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"NotificationTargetARN":%q}`, partitionARN(p, "sqs", r, "hooks"))
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSQSQueue, partitionARN(p, "sqs", r, "hooks")) },
		},
		{
			name: "config rule→lambda", resolve: resolveConfigRelationships, srcType: TypeConfigRule,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"Source":{"Owner":"CUSTOM_LAMBDA","SourceIdentifier":%q}}`, partitionARN(p, "lambda", r, "function:check"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeLambdaFunction, partitionARN(p, "lambda", r, "function:check"))
			},
		},
		{
			name: "connect integration→lambda", resolve: resolveConnectIntegrationAssociationTarget, srcType: TypeConnectIntegrationAssociation,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"IntegrationArn":%q}`, partitionARN(p, "lambda", r, "function:ivr"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeLambdaFunction, partitionARN(p, "lambda", r, "function:ivr"))
			},
		},
		{
			name: "connect integration→lex", resolve: resolveConnectIntegrationAssociationTarget, srcType: TypeConnectIntegrationAssociation,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"IntegrationArn":%q}`, partitionARN(p, "lex", r, "bot-alias/b/a"))
			},
			targets: func(p, r string) []edgeTarget { return one(TypeLexBot, partitionARN(p, "lex", r, "bot-alias/b/a")) },
		},
		{
			name: "connect integration→wisdom", resolve: resolveConnectIntegrationAssociationTarget, srcType: TypeConnectIntegrationAssociation,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"IntegrationArn":%q}`, partitionARN(p, "wisdom", r, "assistant/w1"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeWisdomAssistant, partitionARN(p, "wisdom", r, "assistant/w1"))
			},
		},
		{
			name: "ecs secret→secretsmanager", resolve: resolveECSTaskDefinitionSecrets, srcType: TypeECSTaskDefinition,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"ContainerDefinitions":[{"Secrets":[{"ValueFrom":%q}]}]}`,
					partitionARN(p, "secretsmanager", r, "secret:db-AbCdEf:password::"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeSecretsManagerSecret, partitionARN(p, "secretsmanager", r, "secret:db-AbCdEf"))
			},
		},
		{
			name: "ecs secret→ssm", resolve: resolveECSTaskDefinitionSecrets, srcType: TypeECSTaskDefinition,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"ContainerDefinitions":[{"Secrets":[{"ValueFrom":%q}]}]}`, partitionARN(p, "ssm", r, "parameter/app/key"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeSSMParameter, partitionARN(p, "ssm", r, "parameter/app/key"))
			},
		},
		{
			name: "kms grant→role", resolve: resolveKMSGrants, srcType: TypeKMSGrant,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"GranteePrincipal":%q}`, globalARN(p, "iam", "role/grantee"))
			},
			targets: func(p, _ string) []edgeTarget { return one(TypeIAMRole, globalARN(p, "iam", "role/grantee")) },
		},
		{
			name: "lakeformation resource→s3", resolve: resolveLakeFormationResourceTargets, srcType: TypeLakeFormationResource,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"ResourceArn":%q}`, "arn:"+p+":s3:::lake/raw/")
			},
			targets: func(p, _ string) []edgeTarget { return one(TypeS3Bucket, "arn:"+p+":s3:::lake") },
		},
		{
			name: "mediapackage asset→s3", resolve: resolveMPV1AssetRefs, srcType: TypeMediaPackageAsset,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"SourceArn":%q}`, "arn:"+p+":s3:::media/in/a.smil")
			},
			targets: func(p, _ string) []edgeTarget { return one(TypeS3Bucket, "arn:"+p+":s3:::media") },
		},
		{
			name: "mwaa→s3", resolve: resolveMWAAEnvironmentRefs, srcType: TypeMWAAEnvironment,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"SourceBucketArn":%q}`, "arn:"+p+":s3:::dags")
			},
			targets: func(p, _ string) []edgeTarget { return one(TypeS3Bucket, "arn:"+p+":s3:::dags") },
		},
		{
			name: "logs delivery destination→log group", resolve: resolveLogsRelationships, srcType: TypeLogsDeliveryDest,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DeliveryDestinationConfiguration":{"DestinationResourceArn":%q}}`, partitionARN(p, "logs", r, "log-group:vended:*"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeLogsLogGroup, partitionARN(p, "logs", r, "log-group:vended"))
			},
		},
		{
			name: "logs delivery destination→s3", resolve: resolveLogsRelationships, srcType: TypeLogsDeliveryDest,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"DeliveryDestinationConfiguration":{"DestinationResourceArn":%q}}`, "arn:"+p+":s3:::vended")
			},
			targets: func(p, _ string) []edgeTarget { return one(TypeS3Bucket, "arn:"+p+":s3:::vended") },
		},
		{
			name: "logs delivery destination→firehose", resolve: resolveLogsRelationships, srcType: TypeLogsDeliveryDest,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DeliveryDestinationConfiguration":{"DestinationResourceArn":%q}}`, partitionARN(p, "firehose", r, "deliverystream/v"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeFirehoseDeliveryStream, partitionARN(p, "firehose", r, "deliverystream/v"))
			},
		},
		{
			name: "subscription filter→lambda", resolve: resolveLogsSubscriptionFilterRefs, srcType: TypeLogsSubscriptionFilter,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DestinationArn":%q}`, partitionARN(p, "lambda", r, "function:ship"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeLambdaFunction, partitionARN(p, "lambda", r, "function:ship"))
			},
		},
		{
			name: "subscription filter→kinesis", resolve: resolveLogsSubscriptionFilterRefs, srcType: TypeLogsSubscriptionFilter,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DestinationArn":%q}`, partitionARN(p, "kinesis", r, "stream/ship"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeKinesisStream, partitionARN(p, "kinesis", r, "stream/ship"))
			},
		},
		{
			name: "subscription filter→firehose", resolve: resolveLogsSubscriptionFilterRefs, srcType: TypeLogsSubscriptionFilter,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DestinationArn":%q}`, partitionARN(p, "firehose", r, "deliverystream/ship"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeFirehoseDeliveryStream, partitionARN(p, "firehose", r, "deliverystream/ship"))
			},
		},
		{
			name: "logs destination→kinesis", resolve: resolveLogsDestinationTargets, srcType: TypeLogsDestination,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"TargetArn":%q}`, partitionARN(p, "kinesis", r, "stream/dest"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeKinesisStream, partitionARN(p, "kinesis", r, "stream/dest"))
			},
		},
		{
			name: "logs destination→firehose", resolve: resolveLogsDestinationTargets, srcType: TypeLogsDestination,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"TargetArn":%q}`, partitionARN(p, "firehose", r, "deliverystream/dest"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeFirehoseDeliveryStream, partitionARN(p, "firehose", r, "deliverystream/dest"))
			},
		},
		{
			name: "vpc lattice access log→log group", resolve: resolveVpcLatticeALSRefs, srcType: TypeVpcLatticeAccessLogSubscription,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DestinationArn":%q}`, partitionARN(p, "logs", r, "log-group:lattice:*"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeLogsLogGroup, partitionARN(p, "logs", r, "log-group:lattice"))
			},
		},
		{
			name: "vpc lattice access log→s3", resolve: resolveVpcLatticeALSRefs, srcType: TypeVpcLatticeAccessLogSubscription,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"DestinationArn":%q}`, "arn:"+p+":s3:::lattice")
			},
			targets: func(p, _ string) []edgeTarget { return one(TypeS3Bucket, "arn:"+p+":s3:::lattice") },
		},
		{
			name: "vpc lattice access log→firehose", resolve: resolveVpcLatticeALSRefs, srcType: TypeVpcLatticeAccessLogSubscription,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DestinationArn":%q}`, partitionARN(p, "firehose", r, "deliverystream/lattice"))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeFirehoseDeliveryStream, partitionARN(p, "firehose", r, "deliverystream/lattice"))
			},
		},
		{
			name: "iot audit config→sns", resolve: resolveIoTAccountAuditConfigurationRefs, srcType: TypeIoTAccountAuditConfiguration,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"AuditNotificationTargetConfigurations":{"SNS":{"TargetArn":%q}}}`, partitionARN(p, "sns", r, "audit"))
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSNSTopic, partitionARN(p, "sns", r, "audit")) },
		},
		{
			name: "sfn definition→sns", resolve: resolveSFNRelationships, srcType: TypeSFNStateMachine,
			attrs: func(p, r string) string {
				def := fmt.Sprintf(`{"States":{"Notify":{"Type":"Task","Resource":"arn:%s:states:::sns:publish","Parameters":{"TopicArn":%q}}}}`,
					p, partitionARN(p, "sns", r, "done"))
				return fmt.Sprintf(`{"Definition":%q}`, def)
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSNSTopic, partitionARN(p, "sns", r, "done")) },
		},
		{
			name: "gamelift queue→sns", resolve: resolveGameLiftGameSessionQueueRefs, srcType: TypeGameLiftGameSessionQueue,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"NotificationTarget":%q}`, partitionARN(p, "sns", r, "placements"))
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSNSTopic, partitionARN(p, "sns", r, "placements")) },
		},
		{
			name: "gamelift matchmaking→sns", resolve: resolveGameLiftMatchmakingConfigRefs, srcType: TypeGameLiftMatchmakingConfiguration,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"NotificationTarget":%q}`, partitionARN(p, "sns", r, "matches"))
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSNSTopic, partitionARN(p, "sns", r, "matches")) },
		},
	}
	runARNMatcherCases(t, cases)
}

// TestFMSMemberEIPRewrite_EveryPartition: Firewall Manager names an EIP
// eip-allocation/, the EC2 scanner elastic-ip/; the rewrite must apply to an
// EC2 ARN in every partition.
func TestFMSMemberEIPRewrite_EveryPartition(t *testing.T) {
	for _, pf := range matcherPartitions {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			eip := upsertTestResource(t, st, "aws", acct.ID, TypeEC2EIP,
				partitionARN(pf.partition, "ec2", pf.region, "elastic-ip/eipalloc-1"), pf.region, "{}")
			set := upsertTestResource(t, st, "aws", acct.ID, TypeFMSResourceSet,
				fmsResourceSetARN(pf.partition, pf.region, acct.ID, "rs1"), pf.region,
				fmsSetAttrs(fmsMember(partitionARN(pf.partition, "ec2", pf.region, "eip-allocation/eipalloc-1"))))
			if err := resolveFMSResourceSetMembers(acct, st); err != nil {
				t.Fatalf("resolveFMSResourceSetMembers: %v", err)
			}
			if got := edgeSources(t, st, set, store.RelAttachedTo); len(got) != 1 || got[0] != eip {
				t.Errorf("attached-to edges into the set from %v; want only the elastic-ip row %s", got, eip)
			}
		})
	}
}

// TestIAMPolicyResourceClassifiers_EveryPartition grants one inline policy
// every resource kind classifyPolicyResource recognises, in aws, aws-cn and
// aws-us-gov, and expects an edge to each.
func TestIAMPolicyResourceClassifiers_EveryPartition(t *testing.T) {
	for _, pf := range matcherPartitions {
		t.Run(pf.partition, func(t *testing.T) {
			p, r := pf.partition, pf.region
			st := newTestStore(t)
			acct := partitionAccount(p)
			targets := []struct{ typ, nativeID, ref string }{
				{TypeKMSKey, partitionARN(p, "kms", r, "key/k1"), partitionARN(p, "kms", r, "key/k1")},
				{TypeS3Bucket, "arn:" + p + ":s3:::data", "arn:" + p + ":s3:::data/*"},
				{TypeSecretsManagerSecret, partitionARN(p, "secretsmanager", r, "secret:db-AbCdEf"), partitionARN(p, "secretsmanager", r, "secret:db-AbCdEf")},
				{TypeDynamoDBTable, partitionARN(p, "dynamodb", r, "table/t"), partitionARN(p, "dynamodb", r, "table/t/index/i")},
				{TypeLambdaFunction, partitionARN(p, "lambda", r, "function:f"), partitionARN(p, "lambda", r, "function:f:live")},
				{TypeLogsLogGroup, partitionARN(p, "logs", r, "log-group:g"), partitionARN(p, "logs", r, "log-group:g:*")},
				{TypeSNSTopic, partitionARN(p, "sns", r, "topic"), partitionARN(p, "sns", r, "topic")},
				{TypeSQSQueue, partitionARN(p, "sqs", r, "queue"), partitionARN(p, "sqs", r, "queue")},
				{TypeSSMParameter, partitionARN(p, "ssm", r, "parameter/k"), partitionARN(p, "ssm", r, "parameter/k")},
				{TypeKinesisStream, partitionARN(p, "kinesis", r, "stream/s"), partitionARN(p, "kinesis", r, "stream/s")},
				{TypeECRRepository, partitionARN(p, "ecr", r, "repository/repo"), partitionARN(p, "ecr", r, "repository/repo")},
				{TypeIAMRole, globalARN(p, "iam", "role/worker"), globalARN(p, "iam", "role/worker")},
				{TypeRDSDBInstance, partitionARN(p, "rds", r, "db:main"), partitionARN(p, "rds", r, "db:main")},
				{TypeSFNStateMachine, partitionARN(p, "states", r, "stateMachine:flow"), partitionARN(p, "states", r, "stateMachine:flow")},
				{TypeEventsEventBus, partitionARN(p, "events", r, "event-bus/bus"), partitionARN(p, "events", r, "event-bus/bus")},
				{TypeEventsRule, partitionARN(p, "events", r, "rule/rule"), partitionARN(p, "events", r, "rule/rule")},
				{TypeEFSFileSystem, partitionARN(p, "elasticfilesystem", r, "file-system/fs-1"), partitionARN(p, "elasticfilesystem", r, "file-system/fs-1")},
			}
			var refs, want []string
			for _, tg := range targets {
				want = append(want, upsertTestResource(t, st, "aws", acct.ID, tg.typ, tg.nativeID, r, "{}"))
				refs = append(refs, fmt.Sprintf("%q", tg.ref))
			}
			doc := `{"Statement":[{"Effect":"Allow","Resource":[` + strings.Join(refs, ",") + `]}]}`
			policy := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRolePolicy,
				globalARN(p, "iam", "role/worker/policy/all"), "", inlineRolePolicyAttrs(t, doc))
			if err := resolveIAMPolicyResources(acct, st); err != nil {
				t.Fatalf("resolveIAMPolicyResources: %v", err)
			}
			assertEdgeTargets(t, st, policy, want...)
		})
	}
}

// TestScanIAMAuthDetails_ManagedFlagEveryPartition: an AWS-managed policy is
// one whose IAM ARN has the account segment "aws", in every partition; a
// customer policy in the same partition is not managed.
func TestScanIAMAuthDetails_ManagedFlagEveryPartition(t *testing.T) {
	for _, pf := range matcherPartitions {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			awsManaged := "arn:" + pf.partition + ":iam::aws:policy/ReadOnlyAccess"
			customer := globalARN(pf.partition, "iam", "policy/team")
			handlers := map[string]func(in any) (any, error){
				"GetAccountAuthorizationDetails": func(any) (any, error) {
					return &iam.GetAccountAuthorizationDetailsOutput{Policies: []iamtypes.ManagedPolicyDetail{
						{Arn: ptrStr(awsManaged), PolicyName: ptrStr("ReadOnlyAccess")},
						{Arn: ptrStr(customer), PolicyName: ptrStr("team")},
					}}, nil
				},
				"ListPolicies": func(any) (any, error) { return &iam.ListPoliciesOutput{}, nil },
			}
			client := iam.NewFromConfig(opStubConfig(handlers, iamDenied))
			if _, _, err := scanIAMAuthDetails(context.Background(), client, acct, st, testScanID); err != nil {
				t.Fatalf("scanIAMAuthDetails: %v", err)
			}
			rows := rowsByNativeID(t, st, TypeIAMPolicy)
			for arn, want := range map[string]bool{awsManaged: true, customer: false} {
				row, ok := rows[arn]
				if !ok {
					t.Fatalf("no %s row for %s; rows = %v", TypeIAMPolicy, arn, nativeIDs(rows))
				}
				if row.ManagedByProvider != want {
					t.Errorf("%s ManagedByProvider = %v; want %v", arn, row.ManagedByProvider, want)
				}
			}
		})
	}
}

// ecrImage is a private ECR image URI on endpoint label ecr ("ecr" or
// "ecr-fips") and host suffix, with a digest so the tag/digest cut is
// exercised too.
func ecrImage(ecr, region, suffix, repo string) string {
	return testAccountID + ".dkr." + ecr + "." + region + "." + suffix + "/" + repo + "@sha256:0123"
}

// TestDNSNameParsers_EveryPartition drives every resolver that parses an AWS
// DNS name with the host of the account's partition (amazonaws.com.cn for
// aws-cn), its FIPS form where one exists, and with a foreign host, which
// names nothing the account can reference: the partition's own suffix with a
// region of another partition (foreignRegion).
func TestDNSNameParsers_EveryPartition(t *testing.T) {
	suffix := func(p string) string {
		s, ok := dnsSuffix(p)
		if !ok {
			t.Fatalf("dnsSuffix(%q) unknown", p)
		}
		return s
	}
	repoAt := func(p, r string) []edgeTarget {
		return one(TypeECRRepository, partitionARN(p, "ecr", r, "repository/team/app"))
	}
	none := func(string, string) []edgeTarget { return nil }
	type imageCase struct {
		name    string
		resolve func(*account, *store.Store) error
		srcType string
		attrs   func(image string) string
	}
	images := []imageCase{
		{"ecs image", resolveECSContainerRelationships, TypeECSTaskDefinition, func(img string) string {
			return fmt.Sprintf(`{"ContainerDefinitions":[{"Image":%q}]}`, img)
		}},
		{"apprunner image", resolveAppRunnerServiceTargets, TypeAppRunnerService, func(img string) string {
			return fmt.Sprintf(`{"SourceConfiguration":{"ImageRepository":{"ImageIdentifier":%q,"ImageRepositoryType":"ECR"}}}`, img)
		}},
		{"batch image", resolveBatchJobDefinitionTargets, TypeBatchJobDefinition, func(img string) string {
			return fmt.Sprintf(`{"ContainerProperties":{"Image":%q}}`, img)
		}},
		{"lambda image", resolveLambdaRelationships, TypeLambdaFunction, func(img string) string {
			return fmt.Sprintf(`{"PackageType":"Image","Code":{"ImageUri":%q}}`, img)
		}},
		{"sagemaker model image", resolveSageMakerModelRefs, TypeSageMakerModel, func(img string) string {
			return fmt.Sprintf(`{"PrimaryContainer":{"Image":%q}}`, img)
		}},
		{"sagemaker model package image", resolveSageMakerModelPackage, TypeSageMakerModelPackage, func(img string) string {
			return fmt.Sprintf(`{"InferenceSpecification":{"Containers":[{"Image":%q}]}}`, img)
		}},
	}
	var cases []arnMatcherCase
	for _, ic := range images {
		cases = append(cases,
			arnMatcherCase{
				name: ic.name, resolve: ic.resolve, srcType: ic.srcType,
				attrs:   func(p, r string) string { return ic.attrs(ecrImage("ecr", r, suffix(p), "team/app")) },
				targets: repoAt,
			},
			arnMatcherCase{
				name: ic.name + " foreign host", resolve: ic.resolve, srcType: ic.srcType,
				attrs: func(p, _ string) string {
					return ic.attrs(ecrImage("ecr", foreignRegion(p), suffix(p), "team/app"))
				},
				targets: none,
				decoys:  func(p, _ string) []edgeTarget { return repoAt(p, foreignRegion(p)) },
			},
		)
	}
	sqsURL := func(region, suffix string) string {
		return "https://sqs." + region + "." + suffix + "/" + testAccountID + "/dlq"
	}
	bucketAt := func(name string) func(p, _ string) []edgeTarget {
		return func(p, _ string) []edgeTarget { return one(TypeS3Bucket, "arn:"+p+":s3:::"+name) }
	}
	issuer := func(region, suffix string) string {
		return "https://cognito-idp." + region + "." + suffix + "/" + region + "_pool"
	}
	cases = append(cases,
		arnMatcherCase{
			name: "ecs image fips host", resolve: resolveECSContainerRelationships, srcType: TypeECSTaskDefinition,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"ContainerDefinitions":[{"Image":%q}]}`, ecrImage("ecr-fips", r, suffix(p), "team/app"))
			},
			targets: repoAt,
		},
		arnMatcherCase{
			name: "customer profiles dlq fips host", resolve: resolveCPDomainRefs, srcType: TypeCPDomain,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DeadLetterQueueUrl":%q}`, "https://sqs-fips."+r+"."+suffix(p)+"/"+testAccountID+"/dlq")
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSQSQueue, partitionARN(p, "sqs", r, "dlq")) },
		},
		arnMatcherCase{
			name: "customer profiles dlq", resolve: resolveCPDomainRefs, srcType: TypeCPDomain,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"DeadLetterQueueUrl":%q}`, sqsURL(r, suffix(p)))
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSQSQueue, partitionARN(p, "sqs", r, "dlq")) },
		},
		arnMatcherCase{
			name: "iot topic rule sqs", resolve: resolveIoTTopicRuleActionRefs, srcType: TypeIoTTopicRule,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"Rule":{"Actions":[{"Sqs":{"QueueUrl":%q}}]}}`, sqsURL(r, suffix(p)))
			},
			targets: func(p, r string) []edgeTarget { return one(TypeSQSQueue, partitionARN(p, "sqs", r, "dlq")) },
		},
		arnMatcherCase{
			name: "apigwv2 jwt issuer", resolve: resolveAPIGatewayV2AuthorizerCognito, srcType: TypeAPIGatewayV2Authorizer,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"AuthorizerType":"JWT","JwtConfiguration":{"Issuer":%q}}`, issuer(r, suffix(p)))
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeCognitoUserPool, partitionARN(p, "cognito-idp", r, "userpool/"+r+"_pool"))
			},
		},
		arnMatcherCase{
			name: "apigwv2 jwt issuer foreign host", resolve: resolveAPIGatewayV2AuthorizerCognito, srcType: TypeAPIGatewayV2Authorizer,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"AuthorizerType":"JWT","JwtConfiguration":{"Issuer":%q}}`, issuer(foreignRegion(p), suffix(p)))
			},
			targets: none,
			decoys: func(p, _ string) []edgeTarget {
				return one(TypeCognitoUserPool, partitionARN(p, "cognito-idp", foreignRegion(p), "userpool/"+foreignRegion(p)+"_pool"))
			},
		},
		arnMatcherCase{
			name: "cognito identity pool provider", resolve: resolveCognitoIdentityPoolRelationships, srcType: TypeCognitoIdentityPool,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"Pool":{"CognitoIdentityProviders":[{"ProviderName":%q}]}}`,
					"cognito-idp."+r+"."+suffix(p)+"/"+r+"_pool")
			},
			targets: func(p, r string) []edgeTarget {
				return one(TypeCognitoUserPool, partitionARN(p, "cognito-idp", r, "userpool/"+r+"_pool"))
			},
		},
		arnMatcherCase{
			name: "cloudfront s3 origin", resolve: resolveDistributionOrigins, srcType: TypeCloudFrontDistribution,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"Origins":{"Items":[{"DomainName":%q}]}}`, "site.s3."+r+"."+suffix(p))
			},
			targets: bucketAt("site"),
		},
		arnMatcherCase{
			name: "cloudfront s3 origin foreign host", resolve: resolveDistributionOrigins, srcType: TypeCloudFrontDistribution,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"Origins":{"Items":[{"DomainName":%q}]}}`, "site.s3."+foreignRegion(p)+"."+suffix(p))
			},
			targets: none,
			decoys:  bucketAt("site"),
		},
		arnMatcherCase{
			name: "guardduty ipset s3 url", resolve: resolveGuardDutyRelationships, srcType: TypeGuardDutyIPSet,
			attrs: func(p, r string) string {
				return fmt.Sprintf(`{"Location":%q}`, "https://lists.s3."+r+"."+suffix(p)+"/ips.txt")
			},
			targets: bucketAt("lists"),
		},
		arnMatcherCase{
			name: "guardduty ipset s3 url foreign host", resolve: resolveGuardDutyRelationships, srcType: TypeGuardDutyIPSet,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"Location":%q}`, "https://lists.s3."+foreignRegion(p)+"."+suffix(p)+"/ips.txt")
			},
			targets: none,
			decoys:  bucketAt("lists"),
		},
		arnMatcherCase{
			name: "guardduty ipset s3 path-style foreign host", resolve: resolveGuardDutyRelationships, srcType: TypeGuardDutyIPSet,
			attrs: func(p, _ string) string {
				return fmt.Sprintf(`{"Location":%q}`, "https://s3."+foreignRegion(p)+"."+suffix(p)+"/lists/ips.txt")
			},
			targets: none,
			decoys:  bucketAt("lists"),
		},
	)
	runARNMatcherCases(t, cases)
}
