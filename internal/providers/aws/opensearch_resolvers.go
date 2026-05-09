package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveOpenSearchDomainTargets,
		EdgeDecl{TypeOpenSearchDomain, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeOpenSearchDomain, TypeEC2Subnet, store.RelUses},
		EdgeDecl{TypeOpenSearchDomain, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeOpenSearchDomain, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeOpenSearchDomain, TypeCognitoUserPool, store.RelUses},
		EdgeDecl{TypeOpenSearchDomain, TypeCognitoIdentityPool, store.RelUses},
		EdgeDecl{TypeOpenSearchDomain, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeOpenSearchDomain, TypeLogsLogGroup, store.RelUses},
	)
}

// opensearchDomainAttrs mirrors the verbatim DomainStatus fields used by
// the resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type opensearchDomainAttrs struct {
	VPCOptions *struct {
		VPCId            *string  `json:"VPCId"`
		SubnetIDs        []string `json:"SubnetIDs"`
		SecurityGroupIDs []string `json:"SecurityGroupIDs"`
	} `json:"VPCOptions"`
	EncryptionAtRestOptions *struct {
		Enabled  *bool   `json:"Enabled"`
		KmsKeyID *string `json:"KmsKeyID"`
	} `json:"EncryptionAtRestOptions"`
	CognitoOptions *struct {
		Enabled        *bool   `json:"Enabled"`
		UserPoolID     *string `json:"UserPoolID"`
		IdentityPoolID *string `json:"IdentityPoolID"`
		RoleArn        *string `json:"RoleArn"`
	} `json:"CognitoOptions"`
	LogPublishingOptions map[string]struct {
		CloudWatchLogsLogGroupArn *string `json:"CloudWatchLogsLogGroupArn"`
		Enabled                   *bool   `json:"Enabled"`
	} `json:"LogPublishingOptions"`
}

// resolveOpenSearchDomainTargets emits the domain's outbound edges:
//   - domain → VPC (attached-to) via VPCOptions.VPCId
//   - domain → subnet (uses) per VPCOptions.SubnetIDs[]
//   - domain → security group (uses) per VPCOptions.SecurityGroupIDs[]
//   - domain → KMS key (uses) via EncryptionAtRestOptions.KmsKeyID
//   - domain → Cognito user-pool (uses) via CognitoOptions.UserPoolID
//   - domain → Cognito identity-pool (uses) via CognitoOptions.IdentityPoolID
//   - domain → IAM role (assumes) via CognitoOptions.RoleArn
//   - domain → CloudWatch log-group (uses) per
//     LogPublishingOptions[*].CloudWatchLogsLogGroupArn
//
// FK-safe via per-type id sets + KMS resolve index. Cross-account refs
// and AWS-managed default keys (`alias/aws/*`) skip silently.
func resolveOpenSearchDomainTargets(acct *account, st *store.Store) error {
	domains, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeOpenSearchDomain},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(domains) == 0 {
		return nil
	}

	vpcIDs, err := resourceIDSet(st, acct.ID, TypeEC2VPC)
	if err != nil {
		return err
	}
	subnetIDs, err := resourceIDSet(st, acct.ID, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgIDs, err := resourceIDSet(st, acct.ID, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	userPoolIDs, err := resourceIDSet(st, acct.ID, TypeCognitoUserPool)
	if err != nil {
		return err
	}
	identityPoolIDs, err := resourceIDSet(st, acct.ID, TypeCognitoIdentityPool)
	if err != nil {
		return err
	}
	roleIDs, err := resourceIDSet(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}
	logGroupIDs, err := resourceIDSet(st, acct.ID, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}

	sets := openSearchTargetSets{
		vpcIDs:          vpcIDs,
		subnetIDs:       subnetIDs,
		sgIDs:           sgIDs,
		userPoolIDs:     userPoolIDs,
		identityPoolIDs: identityPoolIDs,
		roleIDs:         roleIDs,
		logGroupIDs:     logGroupIDs,
		kmsIdx:          kmsIdx,
	}
	for _, d := range domains {
		var attrs opensearchDomainAttrs
		if err := json.Unmarshal([]byte(d.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := ""
		if d.Region != nil {
			region = *d.Region
		}
		if err := emitOpenSearchVPCEdges(st, acct, d, region, attrs, sets); err != nil {
			return err
		}
		if err := emitOpenSearchKMSEdge(st, acct, d, region, attrs, sets); err != nil {
			return err
		}
		if err := emitOpenSearchCognitoEdges(st, acct, d, region, attrs, sets); err != nil {
			return err
		}
		if err := emitOpenSearchLogGroupEdges(st, acct, d, attrs, sets); err != nil {
			return err
		}
	}
	return nil
}

// openSearchTargetSets bundles the FK-safe target id sets so the per-domain
// helpers below take a single struct rather than eight maps.
type openSearchTargetSets struct {
	vpcIDs          map[string]struct{}
	subnetIDs       map[string]struct{}
	sgIDs           map[string]struct{}
	userPoolIDs     map[string]struct{}
	identityPoolIDs map[string]struct{}
	roleIDs         map[string]struct{}
	logGroupIDs     map[string]struct{}
	kmsIdx          *kmsResolveIndex
}

func emitOpenSearchVPCEdges(st *store.Store, acct *account, d store.Resource, region string, attrs opensearchDomainAttrs, sets openSearchTargetSets) error {
	if attrs.VPCOptions == nil {
		return nil
	}
	if vpcID := sv(attrs.VPCOptions.VPCId); vpcID != "" {
		vpcARN := ec2ARN(region, acct.ID, "vpc", vpcID)
		vID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vpcARN)
		if _, ok := sets.vpcIDs[vID]; ok {
			if err := st.UpsertRelationship(d.ID, vID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert opensearch domain→vpc: %w", err)
			}
		}
	}
	for _, sn := range attrs.VPCOptions.SubnetIDs {
		if sn == "" {
			continue
		}
		sARN := ec2ARN(region, acct.ID, "subnet", sn)
		sID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, sARN)
		if _, ok := sets.subnetIDs[sID]; ok {
			if err := st.UpsertRelationship(d.ID, sID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert opensearch domain→subnet: %w", err)
			}
		}
	}
	for _, sg := range attrs.VPCOptions.SecurityGroupIDs {
		if sg == "" {
			continue
		}
		sgARN := ec2ARN(region, acct.ID, "security-group", sg)
		sID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
		if _, ok := sets.sgIDs[sID]; ok {
			if err := st.UpsertRelationship(d.ID, sID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert opensearch domain→sg: %w", err)
			}
		}
	}
	return nil
}

func emitOpenSearchKMSEdge(st *store.Store, acct *account, d store.Resource, region string, attrs opensearchDomainAttrs, sets openSearchTargetSets) error {
	if attrs.EncryptionAtRestOptions == nil {
		return nil
	}
	keyRef := sv(attrs.EncryptionAtRestOptions.KmsKeyID)
	if keyRef == "" {
		return nil
	}
	keyID, ok := sets.kmsIdx.resolveKMSKeyID(keyRef, region, acct.ID)
	if !ok {
		return nil
	}
	if err := st.UpsertRelationship(d.ID, keyID, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert opensearch domain→kms: %w", err)
	}
	return nil
}

func emitOpenSearchCognitoEdges(st *store.Store, acct *account, d store.Resource, region string, attrs opensearchDomainAttrs, sets openSearchTargetSets) error {
	if attrs.CognitoOptions == nil {
		return nil
	}
	if upID := sv(attrs.CognitoOptions.UserPoolID); upID != "" {
		upARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", region, acct.ID, upID)
		uID := store.ResourceID("aws", acct.ID, TypeCognitoUserPool, upARN)
		if _, ok := sets.userPoolIDs[uID]; ok {
			if err := st.UpsertRelationship(d.ID, uID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert opensearch domain→user-pool: %w", err)
			}
		}
	}
	if ipID := sv(attrs.CognitoOptions.IdentityPoolID); ipID != "" {
		ipARN := fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/%s", region, acct.ID, ipID)
		iID := store.ResourceID("aws", acct.ID, TypeCognitoIdentityPool, ipARN)
		if _, ok := sets.identityPoolIDs[iID]; ok {
			if err := st.UpsertRelationship(d.ID, iID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert opensearch domain→identity-pool: %w", err)
			}
		}
	}
	if roleARN := sv(attrs.CognitoOptions.RoleArn); roleARN != "" {
		rID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
		if _, ok := sets.roleIDs[rID]; ok {
			if err := st.UpsertRelationship(d.ID, rID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert opensearch domain→iam role: %w", err)
			}
		}
	}
	return nil
}

func emitOpenSearchLogGroupEdges(st *store.Store, acct *account, d store.Resource, attrs opensearchDomainAttrs, sets openSearchTargetSets) error {
	for _, lp := range attrs.LogPublishingOptions {
		arn := strings.TrimSuffix(sv(lp.CloudWatchLogsLogGroupArn), ":*")
		if arn == "" {
			continue
		}
		lID := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, arn)
		if _, ok := sets.logGroupIDs[lID]; ok {
			if err := st.UpsertRelationship(d.ID, lID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert opensearch domain→log-group: %w", err)
			}
		}
	}
	return nil
}
