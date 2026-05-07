package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveCloudFormationStackResources,
		EdgeDecl{TypeCloudFormationStack, TypeS3Bucket, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeIAMRole, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeIAMUser, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeIAMPolicy, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeLambdaFunction, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeLambdaLayerVersion, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeEC2Instance, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeEC2SecurityGroup, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeEC2VPC, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeEC2Subnet, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeDynamoDBTable, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeSNSTopic, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeSQSQueue, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeLogsLogGroup, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeKMSKey, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeSecretsManagerSecret, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeRDSDBInstance, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeRDSDBCluster, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeSFNStateMachine, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeEventsRule, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeEventsEventBus, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeEFSFileSystem, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeECRRepository, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeKinesisStream, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeSSMParameter, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeELBv2LoadBalancer, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeELBv2TargetGroup, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeAPIGatewayRestAPI, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeAPIGatewayV2API, store.RelContains},
		EdgeDecl{TypeCloudFormationStack, TypeCloudFormationStack, store.RelContains},
	)
	registerResolver(
		resolveCloudFormationStackSetInstances,
		EdgeDecl{TypeCloudFormationStackSet, TypeCloudFormationStack, store.RelContains},
	)
}

// cfnStackAttrs mirrors the wrapped attrs persisted by scanCloudFormationStacks.
// Only the fields the resolver consumes are listed.
type cfnStackAttrs struct {
	Resources []struct {
		LogicalResourceID  *string `json:"LogicalResourceID"`
		PhysicalResourceID *string `json:"PhysicalResourceID"`
		ResourceType       *string `json:"ResourceType"`
		ResourceStatus     string  `json:"ResourceStatus"`
	} `json:"Resources"`
}

// cfnStackSetAttrs mirrors stackSetWithInstances.
type cfnStackSetAttrs struct {
	Instances []struct {
		StackID *string `json:"StackID"`
		Account *string `json:"Account"`
		Region  *string `json:"Region"`
	} `json:"Instances"`
}

// cfnTypeBinding maps one CloudFormation ResourceType to a disco type plus
// the function that turns the CloudFormation PhysicalResourceID (which varies
// in shape per service — sometimes the ARN, sometimes a bare name or ID)
// into the disco NativeID. Returning empty string skips this row, used for
// shapes like custom-bus EventBridge rules where physID alone can't rebuild
// the canonical ARN.
type cfnTypeBinding struct {
	discoType  string
	toNativeID func(physID, region, acctID string) string
}

// passthrough returns the physID verbatim, used for services where CloudFormation
// PhysicalResourceID already matches disco NativeID (full ARNs).
func passthrough(physID, _, _ string) string { return physID }

// cfnTypeMap is the dispatch table from `AWS::Service::Resource` to disco type
// + NativeID synthesis. Adding a new service edge is one row here, no
// resolver-level changes needed.
//
// Most AWS services return PhysicalResourceID = full ARN — those use
// passthrough. The rest synthesize from a name or ID using the same ARN
// shape the corresponding disco scanner stores.
var cfnTypeMap = map[string]cfnTypeBinding{
	"AWS::S3::Bucket": {
		discoType: TypeS3Bucket,
		toNativeID: func(name, _, _ string) string {
			if name == "" {
				return ""
			}
			return "arn:aws:s3:::" + name
		},
	},
	"AWS::IAM::Role": {
		discoType: TypeIAMRole,
		toNativeID: func(name, _, acct string) string {
			if name == "" {
				return ""
			}
			return "arn:aws:iam::" + acct + ":role/" + name
		},
	},
	"AWS::IAM::User": {
		discoType: TypeIAMUser,
		toNativeID: func(name, _, acct string) string {
			if name == "" {
				return ""
			}
			return "arn:aws:iam::" + acct + ":user/" + name
		},
	},
	"AWS::IAM::ManagedPolicy": {
		// Managed policies set PhysicalResourceID to the full policy ARN.
		discoType: TypeIAMPolicy, toNativeID: passthrough,
	},
	"AWS::Lambda::Function": {
		discoType: TypeLambdaFunction,
		toNativeID: func(name, region, acct string) string {
			if name == "" {
				return ""
			}
			return "arn:aws:lambda:" + region + ":" + acct + ":function:" + name
		},
	},
	"AWS::Lambda::LayerVersion": {
		// PhysicalResourceID for layer versions is the full layer-version ARN.
		discoType: TypeLambdaLayerVersion, toNativeID: passthrough,
	},
	"AWS::EC2::Instance": {
		discoType: TypeEC2Instance,
		toNativeID: func(id, region, acct string) string {
			if id == "" {
				return ""
			}
			return ec2ARN(region, acct, "instance", id)
		},
	},
	"AWS::EC2::SecurityGroup": {
		discoType: TypeEC2SecurityGroup,
		toNativeID: func(id, region, acct string) string {
			if id == "" {
				return ""
			}
			return ec2ARN(region, acct, "security-group", id)
		},
	},
	"AWS::EC2::VPC": {
		discoType: TypeEC2VPC,
		toNativeID: func(id, region, acct string) string {
			if id == "" {
				return ""
			}
			return ec2ARN(region, acct, "vpc", id)
		},
	},
	"AWS::EC2::Subnet": {
		discoType: TypeEC2Subnet,
		toNativeID: func(id, region, acct string) string {
			if id == "" {
				return ""
			}
			return ec2ARN(region, acct, "subnet", id)
		},
	},
	"AWS::DynamoDB::Table": {
		discoType: TypeDynamoDBTable,
		toNativeID: func(name, region, acct string) string {
			if name == "" {
				return ""
			}
			return "arn:aws:dynamodb:" + region + ":" + acct + ":table/" + name
		},
	},
	"AWS::SNS::Topic": {discoType: TypeSNSTopic, toNativeID: passthrough},
	"AWS::SQS::Queue": {
		// PhysicalResourceID for SQS is the queue URL —
		// https://sqs.{region}.amazonaws.com/{acct}/{name} — but disco's
		// queue NativeID is the ARN. Take the trailing path segment as the
		// queue name; region/acct come from the stack row itself, not the URL.
		discoType: TypeSQSQueue,
		toNativeID: func(url, region, acct string) string {
			if url == "" {
				return ""
			}
			i := strings.LastIndex(url, "/")
			if i < 0 || i == len(url)-1 {
				return ""
			}
			return "arn:aws:sqs:" + region + ":" + acct + ":" + url[i+1:]
		},
	},
	"AWS::Logs::LogGroup": {
		discoType: TypeLogsLogGroup,
		toNativeID: func(name, region, acct string) string {
			if name == "" {
				return ""
			}
			return logGroupNativeIDFromName(acct, region, name)
		},
	},
	"AWS::KMS::Key": {
		discoType: TypeKMSKey,
		toNativeID: func(id, region, acct string) string {
			if id == "" {
				return ""
			}
			return "arn:aws:kms:" + region + ":" + acct + ":key/" + id
		},
	},
	"AWS::SecretsManager::Secret": {discoType: TypeSecretsManagerSecret, toNativeID: passthrough},
	"AWS::RDS::DBInstance": {
		discoType: TypeRDSDBInstance,
		toNativeID: func(id, region, acct string) string {
			if id == "" {
				return ""
			}
			return rdsARN(region, acct, "db", id)
		},
	},
	"AWS::RDS::DBCluster": {
		discoType: TypeRDSDBCluster,
		toNativeID: func(id, region, acct string) string {
			if id == "" {
				return ""
			}
			return rdsARN(region, acct, "cluster", id)
		},
	},
	"AWS::StepFunctions::StateMachine": {discoType: TypeSFNStateMachine, toNativeID: passthrough},
	"AWS::Events::Rule": {
		// Custom-bus rules set PhysicalResourceID to a bare name with no
		// embedded bus reference, so we cannot reconstruct the canonical
		// ARN. Skip those (return empty); only default-bus rules resolve.
		// Default-bus name is just the rule name; custom-bus shows up as
		// `BusName|RuleName` in some templates — reject the pipe form too.
		discoType: TypeEventsRule,
		toNativeID: func(name, region, acct string) string {
			if name == "" || strings.ContainsAny(name, "|") {
				return ""
			}
			return "arn:aws:events:" + region + ":" + acct + ":rule/" + name
		},
	},
	"AWS::Events::EventBus": {
		discoType: TypeEventsEventBus,
		toNativeID: func(name, region, acct string) string {
			if name == "" {
				return ""
			}
			return "arn:aws:events:" + region + ":" + acct + ":event-bus/" + name
		},
	},
	"AWS::EFS::FileSystem": {
		discoType: TypeEFSFileSystem,
		toNativeID: func(id, region, acct string) string {
			if id == "" {
				return ""
			}
			return "arn:aws:elasticfilesystem:" + region + ":" + acct + ":file-system/" + id
		},
	},
	"AWS::ECR::Repository": {
		discoType: TypeECRRepository,
		toNativeID: func(name, region, acct string) string {
			if name == "" {
				return ""
			}
			return "arn:aws:ecr:" + region + ":" + acct + ":repository/" + name
		},
	},
	"AWS::Kinesis::Stream": {
		discoType: TypeKinesisStream,
		toNativeID: func(name, region, acct string) string {
			if name == "" {
				return ""
			}
			return "arn:aws:kinesis:" + region + ":" + acct + ":stream/" + name
		},
	},
	"AWS::SSM::Parameter": {
		// CloudFormation's PhysicalResourceID for SSM parameters drops the
		// leading slash that bare names have in the API. Disco's SSM scanner
		// stores the canonical ARN with `parameter/{name}` (no double slash).
		discoType: TypeSSMParameter,
		toNativeID: func(name, region, acct string) string {
			if name == "" {
				return ""
			}
			return "arn:aws:ssm:" + region + ":" + acct + ":parameter/" + strings.TrimPrefix(name, "/")
		},
	},
	"AWS::ElasticLoadBalancingV2::LoadBalancer": {discoType: TypeELBv2LoadBalancer, toNativeID: passthrough},
	"AWS::ElasticLoadBalancingV2::TargetGroup":  {discoType: TypeELBv2TargetGroup, toNativeID: passthrough},
	"AWS::ApiGateway::RestApi": {
		discoType: TypeAPIGatewayRestAPI,
		toNativeID: func(id, region, _ string) string {
			if id == "" {
				return ""
			}
			return apigatewayARN(region, "restapis", id)
		},
	},
	"AWS::ApiGatewayV2::Api": {
		discoType: TypeAPIGatewayV2API,
		toNativeID: func(id, region, _ string) string {
			if id == "" {
				return ""
			}
			return "arn:aws:apigateway:" + region + "::/apis/" + id
		},
	},
	"AWS::CloudFormation::Stack": {
		// Nested-stack PhysicalResourceID is the child stack's full ARN.
		discoType: TypeCloudFormationStack, toNativeID: passthrough,
	},
}

// skipResourceStatus lists statuses where the underlying AWS resource
// either never existed (CREATE_FAILED) or no longer exists (DELETE_*),
// so emitting a contains edge would point at a phantom target.
var skipResourceStatus = map[string]bool{
	"CREATE_FAILED":      true,
	"DELETE_COMPLETE":    true,
	"DELETE_IN_PROGRESS": true,
	"DELETE_SKIPPED":     true,
	"DELETE_FAILED":      true,
}

// resolveCloudFormationStackResources walks every scanned stack's embedded
// resource list and emits one `contains` edge per managed resource whose
// disco target is also scanned. FK-safe: cross-account resources, AWS
// service-internal resources, and types not in cfnTypeMap silently skip.
func resolveCloudFormationStackResources(acct *account, st *store.Store) error {
	stacks, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeCloudFormationStack},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(stacks) == 0 {
		return nil
	}

	known, err := cfnTargetIDSet(acct, st)
	if err != nil {
		return err
	}

	for _, s := range stacks {
		var attrs cfnStackAttrs
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(s.Region)
		for _, res := range attrs.Resources {
			physID := sv(res.PhysicalResourceID)
			rtype := sv(res.ResourceType)
			if physID == "" || rtype == "" {
				continue
			}
			if skipResourceStatus[res.ResourceStatus] {
				continue
			}
			binding, ok := cfnTypeMap[rtype]
			if !ok {
				continue
			}
			nativeID := binding.toNativeID(physID, region, acct.ID)
			if nativeID == "" {
				continue
			}
			tID := store.ResourceID("aws", acct.ID, binding.discoType, nativeID)
			if !known[tID] {
				continue
			}
			if err := st.UpsertRelationship(s.ID, tID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudformation stack→%s: %w", binding.discoType, err)
			}
		}
	}
	return nil
}

// resolveCloudFormationStackSetInstances emits a `contains` edge from each
// stack-set to every deployed stack instance whose StackID is in the scan.
// Cross-account / cross-region instances skip silently — the deployed stack
// only exists in disco's graph if the scanner credentials covered that
// account+region.
func resolveCloudFormationStackSetInstances(acct *account, st *store.Store) error {
	sets, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeCloudFormationStackSet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(sets) == 0 {
		return nil
	}

	stackIDs, err := cfnStackIDSet(st)
	if err != nil {
		return err
	}

	for _, ss := range sets {
		var attrs cfnStackSetAttrs
		if err := json.Unmarshal([]byte(ss.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, inst := range attrs.Instances {
			arn := sv(inst.StackID)
			instAcct := sv(inst.Account)
			if arn == "" || instAcct == "" {
				continue
			}
			tID := store.ResourceID("aws", instAcct, TypeCloudFormationStack, arn)
			if !stackIDs[tID] {
				continue
			}
			if err := st.UpsertRelationship(ss.ID, tID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudformation stack-set→stack: %w", err)
			}
		}
	}
	return nil
}

// cfnTargetIDSet pre-builds the membership set covering every disco type
// referenced from cfnTypeMap, so the resolver answers FK-existence in a
// single map lookup per resource. One ListResources call instead of one
// per type.
func cfnTargetIDSet(acct *account, st *store.Store) (map[string]bool, error) {
	seen := make(map[string]struct{}, len(cfnTypeMap))
	types := make([]string, 0, len(cfnTypeMap))
	for _, b := range cfnTypeMap {
		if _, dup := seen[b.discoType]; dup {
			continue
		}
		seen[b.discoType] = struct{}{}
		types = append(types, b.discoType)
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: types,
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(rows))
	for _, r := range rows {
		m[r.ID] = true
	}
	return m, nil
}

// cfnStackIDSet returns the membership set of every scanned stack across
// all accounts — stack-set instances often deploy into accounts other than
// the one running the scan, so this is account-unfiltered intentionally.
func cfnStackIDSet(st *store.Store) (map[string]bool, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws",
		Types:    []string{TypeCloudFormationStack},
		Limit:    util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(rows))
	for _, r := range rows {
		m[r.ID] = true
	}
	return m, nil
}
