package aws

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// resolveLambdaAll runs every Lambda sub-resolver in sequence, stopping at
// the first error.
func resolveLambdaAll(acct *account, st *store.Store) error {
	if err := resolveLambdaRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveLambdaAliasRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveLambdaVersionRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveLambdaESMRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveLambdaEventInvokeConfigRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveLambdaFunctionURLRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveLambdaCodeSigningConfigRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveLambdaLayerRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveLambdaPermissionRelationships(acct, st); err != nil {
		return err
	}
	return resolveLambdaLayerVersionPermissionRelationships(acct, st)
}

func init() {
	registerResolver(
		resolveLambdaAll,
		EdgeDecl{TypeLambdaFunction, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeLambdaFunction, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeLambdaFunction, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeLambdaFunction, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeLambdaFunction, TypeEFSAccessPoint, store.RelUses},
		EdgeDecl{TypeLambdaFunction, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeLambdaFunction, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeLambdaFunction, TypeECRRepository, store.RelUses},
		EdgeDecl{TypeLambdaAlias, TypeLambdaFunction, store.RelAttachedTo},
		EdgeDecl{TypeLambdaVersion, TypeLambdaFunction, store.RelAttachedTo},
		EdgeDecl{TypeLambdaESM, TypeLambdaFunction, store.RelAttachedTo},
		EdgeDecl{TypeLambdaESM, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeLambdaESM, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypeLambdaESM, TypeMSKCluster, store.RelUses},
		EdgeDecl{TypeLambdaESM, TypeMQBroker, store.RelUses},
		EdgeDecl{TypeLambdaESM, TypeDocDBCluster, store.RelUses},
		EdgeDecl{TypeLambdaEventInvokeConfig, TypeLambdaFunction, store.RelAttachedTo},
		EdgeDecl{TypeLambdaEventInvokeConfig, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeLambdaEventInvokeConfig, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeLambdaEventInvokeConfig, TypeEventsEventBus, store.RelUses},
		EdgeDecl{TypeLambdaEventInvokeConfig, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeLambdaURL, TypeLambdaFunction, store.RelAttachedTo},
		EdgeDecl{TypeLambdaFunction, TypeLambdaCodeSigningConfig, store.RelUses},
		EdgeDecl{TypeLambdaFunction, TypeLambdaLayerVersion, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeLambdaFunction, store.RelAttachedTo},
		// Permission policy SourceArn principals dispatched via
		// classifyPolicyResource — declare every type that helper may
		// return so the audit tool sees the edges as expected.
		EdgeDecl{TypeLambdaPermission, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeSecretsManagerSecret, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeDynamoDBTable, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeLogsLogGroup, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeSSMParameter, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeECRRepository, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeIAMServiceLinkedRole, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeRDSDBInstance, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeRDSDBCluster, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeSFNStateMachine, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeEventsEventBus, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeEventsRule, store.RelUses},
		EdgeDecl{TypeLambdaPermission, TypeEFSFileSystem, store.RelUses},
		EdgeDecl{TypeLambdaLayerVersionPermission, TypeLambdaLayerVersion, store.RelAttachedTo},
	)
}

// lambdaStripQualifier strips the version or alias qualifier from a qualified
// Lambda ARN, returning the unqualified function ARN.
// "arn:aws:lambda:{r}:{acct}:function:{name}:{qualifier}" → "arn:aws:lambda:{r}:{acct}:function:{name}"
// Unqualified ARNs are returned unchanged.
func lambdaStripQualifier(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) == 8 {
		return strings.Join(parts[:7], ":")
	}
	return arn
}

// resolveLambdaRelationships links each function to its IAM execution role,
// VPC subnets and security groups when the function is VPC-attached, the
// KMS key used to encrypt its environment variables (customer-managed only),
// the dead-letter target (SQS / SNS), and the ECR repository for
// image-package functions.
func resolveLambdaRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	// FK-safe id sets for cross-service targets that may be unscanned.
	sqsSet, err := scannedIDSet(acct, st, TypeSQSQueue)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	ecrSet, err := scannedIDSet(acct, st, TypeECRRepository)
	if err != nil {
		return err
	}
	for _, r := range fns {
		var attrs struct {
			Role      *string `json:"Role"` // IAM role ARN
			KMSKeyArn *string `json:"KMSKeyArn"`
			VpcConfig *struct {
				SubnetIDs        []string `json:"SubnetIDs"`
				SecurityGroupIDs []string `json:"SecurityGroupIDs"`
			} `json:"VpcConfig"`
			FileSystemConfigs []struct {
				Arn *string `json:"Arn"` // EFS access point ARN
			} `json:"FileSystemConfigs"`
			DeadLetterConfig *struct {
				TargetArn *string `json:"TargetArn"` // SQS queue or SNS topic
			} `json:"DeadLetterConfig"`
			Code *struct {
				ImageURI *string `json:"ImageUri"` // ECR image URI for image-package functions
			} `json:"Code"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.Role != nil {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, *attrs.Role)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda→role relationship: %w", err)
			}
		}
		// Function → KMS (customer-managed env-var encryption)
		if sv(attrs.KMSKeyArn) != "" {
			keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, *attrs.KMSKeyArn)
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda→kms relationship: %w", err)
			}
		}
		if attrs.VpcConfig != nil {
			for _, sn := range attrs.VpcConfig.SubnetIDs {
				if sn == "" {
					continue
				}
				subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
				if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert lambda→subnet relationship: %w", err)
				}
			}
			for _, sg := range attrs.VpcConfig.SecurityGroupIDs {
				if sg == "" {
					continue
				}
				sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
				if err := st.UpsertRelationship(r.ID, sgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lambda→security-group relationship: %w", err)
				}
			}
		}
		// Function → EFS access point (mounted file system)
		for _, fs := range attrs.FileSystemConfigs {
			if sv(fs.Arn) == "" {
				continue
			}
			apID := store.ResourceID("aws", acct.ID, TypeEFSAccessPoint, *fs.Arn)
			if err := st.UpsertRelationship(r.ID, apID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda→efs-access-point relationship: %w", err)
			}
		}
		// Function → dead-letter target (SQS queue or SNS topic).
		if attrs.DeadLetterConfig != nil && sv(attrs.DeadLetterConfig.TargetArn) != "" {
			if err := emitLambdaSQSOrSNSEdge(st, r.ID, *attrs.DeadLetterConfig.TargetArn, acct.ID, sqsSet, snsSet); err != nil {
				return fmt.Errorf("upsert lambda→dlq relationship: %w", err)
			}
		}
		// Function → ECR repository for image-package functions.
		if attrs.Code != nil && sv(attrs.Code.ImageURI) != "" {
			if repoARN := apprunnerImageToRepoARN(*attrs.Code.ImageURI); repoARN != "" {
				repoID := store.ResourceID("aws", acct.ID, TypeECRRepository, repoARN)
				if ecrSet[repoID] {
					if err := st.UpsertRelationship(r.ID, repoID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert lambda→ecr-repository relationship: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// emitLambdaSQSOrSNSEdge dispatches an SQS or SNS ARN to the appropriate
// resource type and upserts a Uses edge. FK-safe via id sets — silent skip
// when the target is not scanned.
func emitLambdaSQSOrSNSEdge(st *store.Store, srcID, targetARN, acctID string, sqsSet, snsSet map[string]bool) error {
	parts := strings.Split(targetARN, ":")
	if len(parts) < 6 {
		return nil
	}
	var tgtID string
	switch parts[2] {
	case "sqs":
		tgtID = store.ResourceID("aws", acctID, TypeSQSQueue, targetARN)
		if !sqsSet[tgtID] {
			return nil
		}
	case "sns":
		tgtID = store.ResourceID("aws", acctID, TypeSNSTopic, targetARN)
		if !snsSet[tgtID] {
			return nil
		}
	default:
		return nil
	}
	return st.UpsertRelationship(srcID, tgtID, store.RelUses, "directed", nil)
}

// resolveLambdaAliasRelationships links each alias to its parent function.
// The alias NativeID is a qualified ARN; stripping the qualifier yields the
// function ARN.
func resolveLambdaAliasRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaAlias},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		fnARN := lambdaStripQualifier(r.NativeID)
		if fnARN == r.NativeID {
			continue // no qualifier to strip; skip
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
		if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda alias→function: %w", err)
		}
	}
	return nil
}

// resolveLambdaVersionRelationships links each published version to its parent
// function. The version NativeID is a qualified ARN ending in the version number.
func resolveLambdaVersionRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaVersion},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		fnARN := lambdaStripQualifier(r.NativeID)
		if fnARN == r.NativeID {
			continue
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
		if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda version→function: %w", err)
		}
	}
	return nil
}

// resolveLambdaESMRelationships links each event source mapping to its target
// function. The FunctionArn in the ESM attributes may be qualified; the
// qualifier is stripped to obtain the base function ARN.
func resolveLambdaESMRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaESM},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		var attrs struct {
			FunctionArn    *string `json:"FunctionArn"`
			EventSourceArn *string `json:"EventSourceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		fnARN := lambdaStripQualifier(sv(attrs.FunctionArn))
		if fnARN != "" {
			fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
			if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda ESM→function: %w", err)
			}
		}
		// ESM → source resource (DynamoDB stream, Kinesis stream, SQS queue).
		// Parse the source ARN's service prefix to pick the right resource type.
		srcARN := sv(attrs.EventSourceArn)
		if srcARN == "" {
			continue
		}
		srcType := lambdaESMSourceType(srcARN)
		if srcType == "" {
			continue // unsupported/unknown source (e.g. self-managed Kafka bootstrap server, DocumentDB)
		}
		srcID := store.ResourceID("aws", acct.ID, srcType, srcARN)
		if err := st.UpsertRelationship(r.ID, srcID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda ESM→source relationship: %w", err)
		}
	}
	return nil
}

// lambdaESMSourceType maps an EventSourceArn to the disco resource type of the
// source. Returns "" when the source service isn't scanned by disco.
func lambdaESMSourceType(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return ""
	}
	// parts[2] = service, parts[5] = "resource-type/..." or just "stream/..."
	switch parts[2] {
	case "sqs":
		// SQS event source ARN is the queue ARN directly.
		return TypeSQSQueue
	case "kinesis":
		// Kinesis event source ARN is the stream ARN directly.
		return TypeKinesisStream
	case "kafka":
		// MSK event source ARN is the cluster ARN directly.
		return TypeMSKCluster
	case "mq":
		// Amazon MQ event source ARN is the broker ARN directly.
		return TypeMQBroker
	case "rds":
		// DocumentDB clusters use the rds: ARN prefix (historical artefact —
		// see internal/providers/aws/CLAUDE.md). ESM only accepts DocDB
		// clusters here, never plain RDS / Neptune; the cluster segment is
		// the disambiguator. NativeID matches docdb_scanners.go.
		if len(parts) >= 7 && parts[5] == "cluster" {
			return TypeDocDBCluster
		}
		return ""
	}
	// DynamoDB streams have their own ARNs that don't match the parent table's
	// ARN the scanner stores; skip until we scan streams natively.
	// Self-managed Kafka uses SelfManagedEventSource.Endpoints (no
	// EventSourceArn) and has no AWS-side resource to link.
	return ""
}

// resolveLambdaEventInvokeConfigRelationships links each async invocation config
// to its parent function and to any OnSuccess / OnFailure destinations
// (SQS queue, SNS topic, EventBridge bus, Lambda function).
func resolveLambdaEventInvokeConfigRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaEventInvokeConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	sqsSet, err := scannedIDSet(acct, st, TypeSQSQueue)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	busSet, err := scannedIDSet(acct, st, TypeEventsEventBus)
	if err != nil {
		return err
	}
	fnSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	for _, r := range resources {
		fnARN := lambdaStripQualifier(r.NativeID)
		if fnARN != r.NativeID {
			fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
			if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda event-invoke-config→function: %w", err)
			}
		}
		var attrs struct {
			DestinationConfig *struct {
				OnSuccess *struct {
					Destination *string `json:"Destination"`
				} `json:"OnSuccess"`
				OnFailure *struct {
					Destination *string `json:"Destination"`
				} `json:"OnFailure"`
			} `json:"DestinationConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DestinationConfig == nil {
			continue
		}
		var dests []string
		if attrs.DestinationConfig.OnSuccess != nil {
			dests = append(dests, sv(attrs.DestinationConfig.OnSuccess.Destination))
		}
		if attrs.DestinationConfig.OnFailure != nil {
			dests = append(dests, sv(attrs.DestinationConfig.OnFailure.Destination))
		}
		for _, dest := range dests {
			if dest == "" {
				continue
			}
			if err := emitLambdaDestinationEdge(st, r.ID, dest, acct.ID, sqsSet, snsSet, busSet, fnSet); err != nil {
				return fmt.Errorf("upsert lambda event-invoke-config→destination: %w", err)
			}
		}
	}
	return nil
}

// emitLambdaDestinationEdge dispatches an async-invocation destination ARN
// (SQS / SNS / EventBridge bus / Lambda function) and upserts a Uses edge.
// FK-safe via id sets — silent skip when target unscanned.
func emitLambdaDestinationEdge(st *store.Store, srcID, destARN, acctID string, sqsSet, snsSet, busSet, fnSet map[string]bool) error {
	parts := strings.Split(destARN, ":")
	if len(parts) < 6 {
		return nil
	}
	var tgtType string
	var set map[string]bool
	switch parts[2] {
	case "sqs":
		tgtType, set = TypeSQSQueue, sqsSet
	case "sns":
		tgtType, set = TypeSNSTopic, snsSet
	case "events":
		tgtType, set = TypeEventsEventBus, busSet
	case "lambda":
		// Strip alias / version qualifier from function ARN before lookup.
		destARN = lambdaStripQualifier(destARN)
		tgtType, set = TypeLambdaFunction, fnSet
	default:
		return nil
	}
	tgtID := store.ResourceID("aws", acctID, tgtType, destARN)
	if !set[tgtID] {
		return nil
	}
	return st.UpsertRelationship(srcID, tgtID, store.RelUses, "directed", nil)
}

// resolveLambdaFunctionURLRelationships links each function URL config to its
// parent function. The NativeID is a qualified FunctionArn.
func resolveLambdaFunctionURLRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaURL},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		fnARN := lambdaStripQualifier(r.NativeID)
		if fnARN == r.NativeID {
			continue
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
		if err := st.UpsertRelationship(r.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda url→function: %w", err)
		}
	}
	return nil
}

// resolveLambdaCodeSigningConfigRelationships links each function that has a
// code signing config to that config via a "uses" relationship.
// CodeSigningConfigArn is extracted from the function's AttributesJSON.
func resolveLambdaCodeSigningConfigRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range fns {
		var attrs struct {
			CodeSigningConfigArn *string `json:"CodeSigningConfigArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		cscARN := sv(attrs.CodeSigningConfigArn)
		if cscARN == "" {
			continue
		}
		cscID := store.ResourceID("aws", acct.ID, TypeLambdaCodeSigningConfig, cscARN)
		if err := st.UpsertRelationship(r.ID, cscID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda function→code-signing-config: %w", err)
		}
	}
	return nil
}

// resolveLambdaLayerRelationships links each function to the layer versions it
// uses. Layer ARNs are extracted from the Layers array in the function's
// AttributesJSON. FK-safe via id-set lookup — functions referencing
// AWS-managed / cross-account layers that have not been scanned (scanner
// only enumerates caller-account layers via ListLayers, plus foreign-acct
// layers reached through scanLambdaForeignLayers) silently skip the edge
// rather than blowing the FK on UpsertRelationship.
func resolveLambdaLayerRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	layerSet, err := scannedIDSet(acct, st, TypeLambdaLayerVersion)
	if err != nil {
		return err
	}
	for _, r := range fns {
		var attrs struct {
			Layers []struct {
				Arn *string `json:"Arn"`
			} `json:"Layers"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, layer := range attrs.Layers {
			layerARN := sv(layer.Arn)
			if layerARN == "" {
				continue
			}
			layerID := store.ResourceID("aws", acct.ID, TypeLambdaLayerVersion, layerARN)
			if !layerSet[layerID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, layerID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda function→layer-version: %w", err)
			}
		}
	}
	return nil
}

// resolveLambdaPermissionRelationships links each aws:lambda:permission row
// (one per function with a non-empty resource policy) to its parent function
// and to every SourceArn principal referenced by an Allow statement's
// Condition block. SourceArn classifications reuse classifyPolicyResource
// from iam_resolvers.go — wildcards, cross-account refs, and unscanned
// targets skip FK-safe.
func resolveLambdaPermissionRelationships(acct *account, st *store.Store) error {
	perms, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaPermission},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(perms) == 0 {
		return nil
	}
	sets, err := loadPolicyResourceSets(acct, st)
	if err != nil {
		return err
	}
	for _, p := range perms {
		// Permission NativeID = "{functionArn}/policy". Strip suffix to
		// recover the parent function ARN; emit AttachedTo edge.
		fnARN := strings.TrimSuffix(p.NativeID, "/policy")
		if fnARN != p.NativeID {
			fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
			if err := st.UpsertRelationship(p.ID, fnID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda permission→function: %w", err)
			}
		}
		// AttributesJSON is the GetPolicy response — {Policy: "<json>"}.
		// Lambda's Policy field is a plain JSON string (not URL-encoded
		// like IAM's), but QueryUnescape is idempotent on plain JSON.
		var wrap struct {
			Policy *string `json:"Policy"`
		}
		if err := json.Unmarshal([]byte(p.AttributesJSON), &wrap); err != nil || sv(wrap.Policy) == "" {
			continue
		}
		decoded, err := url.QueryUnescape(*wrap.Policy)
		if err != nil {
			decoded = *wrap.Policy
		}
		var doc struct {
			Statement lambdaPermStmtList `json:"Statement"`
		}
		if err := json.Unmarshal([]byte(decoded), &doc); err != nil {
			continue
		}
		region := ""
		if p.Region != nil {
			region = *p.Region
		}
		for _, stmt := range doc.Statement {
			if !strings.EqualFold(stmt.Effect, "Allow") {
				continue
			}
			for _, srcARN := range lambdaPermSourceArns(stmt) {
				targetID, ok := classifyPolicyResource(srcARN, region, acct.ID, sets)
				if !ok {
					continue
				}
				if err := st.UpsertRelationship(p.ID, targetID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lambda permission→source: %w", err)
				}
			}
		}
	}
	return nil
}

// lambdaPermStmt mirrors the subset of policy-statement fields the Lambda
// permission resolver inspects. Distinct from iam_resolvers.go's policyStmt
// (which only exposes Effect + Resource) — Lambda permission docs carry
// the principal SourceArn in a Condition block, not in Resource.
type lambdaPermStmt struct {
	Effect    string                                `json:"Effect"`
	Condition map[string]map[string]json.RawMessage `json:"Condition"`
}

// lambdaPermStmtList accepts a Statement field that may be a single object
// OR an array — same shape as iam_resolvers.go's statementList.
type lambdaPermStmtList []lambdaPermStmt

func (s *lambdaPermStmtList) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		var arr []lambdaPermStmt
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		*s = arr
		return nil
	}
	var one lambdaPermStmt
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*s = []lambdaPermStmt{one}
	return nil
}

// lambdaPermSourceArns returns every "AWS:SourceArn" value found in a
// statement's Condition block across the operators Lambda permission docs
// commonly use (ArnLike / ArnEquals / StringLike / StringEquals). Each
// operator maps a context key to a string OR string-array; we accept both.
// Condition keys are case-insensitive in IAM, so we match keys ignoring
// case.
func lambdaPermSourceArns(stmt lambdaPermStmt) []string {
	if len(stmt.Condition) == 0 {
		return nil
	}
	var out []string
	for op, kv := range stmt.Condition {
		opLower := strings.ToLower(op)
		switch opLower {
		case "arnlike", "arnequals", "stringlike", "stringequals":
		default:
			continue
		}
		for k, raw := range kv {
			if !strings.EqualFold(k, "AWS:SourceArn") {
				continue
			}
			var arr []string
			if err := json.Unmarshal(raw, &arr); err == nil {
				out = append(out, arr...)
				continue
			}
			var one string
			if err := json.Unmarshal(raw, &one); err == nil && one != "" {
				out = append(out, one)
			}
		}
	}
	return out
}

// resolveLambdaLayerVersionPermissionRelationships links each
// aws:lambda:layer-version-permission row to its parent layer version. The
// principal side of the policy is always an AWS account ID (or "*"), which
// is not edge-bearing — only the AttachedTo edge is emitted.
func resolveLambdaLayerVersionPermissionRelationships(acct *account, st *store.Store) error {
	perms, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaLayerVersionPermission},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, p := range perms {
		layerARN := strings.TrimSuffix(p.NativeID, "/policy")
		if layerARN == p.NativeID {
			continue
		}
		layerID := store.ResourceID("aws", acct.ID, TypeLambdaLayerVersion, layerARN)
		if err := st.UpsertRelationship(p.ID, layerID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lambda layer-version-permission→layer-version: %w", err)
		}
	}
	return nil
}
