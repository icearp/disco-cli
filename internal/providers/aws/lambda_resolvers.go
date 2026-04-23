package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(func(acct *account, st *store.Store) error {
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
		return resolveLambdaLayerRelationships(acct, st)
	})
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
// VPC subnets and security groups when the function is VPC-attached, and the
// KMS key used to encrypt its environment variables (customer-managed only).
func resolveLambdaRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range fns {
		var attrs struct {
			Role      *string `json:"Role"` // IAM role ARN
			KMSKeyArn *string `json:"KMSKeyArn"`
			VpcConfig *struct {
				SubnetIds        []string `json:"SubnetIds"`
				SecurityGroupIds []string `json:"SecurityGroupIds"`
			} `json:"VpcConfig"`
			FileSystemConfigs []struct {
				Arn *string `json:"Arn"` // EFS access point ARN
			} `json:"FileSystemConfigs"`
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
			for _, sn := range attrs.VpcConfig.SubnetIds {
				if sn == "" {
					continue
				}
				subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
				if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert lambda→subnet relationship: %w", err)
				}
			}
			for _, sg := range attrs.VpcConfig.SecurityGroupIds {
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
	}
	return nil
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
			continue // unsupported/unknown source (e.g. Kafka bootstrap server, DocumentDB)
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
	}
	// DynamoDB streams have their own ARNs that don't match the parent table's
	// ARN the scanner stores; skip until we scan streams natively.
	return ""
}

// resolveLambdaEventInvokeConfigRelationships links each async invocation config
// to its parent function. The NativeID is a qualified FunctionArn.
func resolveLambdaEventInvokeConfigRelationships(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaEventInvokeConfig},
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
			return fmt.Errorf("upsert lambda event-invoke-config→function: %w", err)
		}
	}
	return nil
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
// AttributesJSON.
func resolveLambdaLayerRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Limit: util.AllResources,
	})
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
			if err := st.UpsertRelationship(r.ID, layerID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda function→layer-version: %w", err)
			}
		}
	}
	return nil
}
