package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerResolver(
		resolveInstanceProfileRoles,
		EdgeDecl{TypeIAMRole, TypeIAMInstanceProfile, store.RelContains},
	)
	registerResolver(
		resolveInlinePolicyParents,
		EdgeDecl{TypeIAMRole, TypeIAMRolePolicy, store.RelContains},
		EdgeDecl{TypeIAMServiceLinkedRole, TypeIAMRolePolicy, store.RelContains},
		EdgeDecl{TypeIAMUser, TypeIAMUserPolicy, store.RelContains},
		EdgeDecl{TypeIAMGroup, TypeIAMGroupPolicy, store.RelContains},
	)
	registerResolver(
		resolveAccessKeyUsers,
		EdgeDecl{TypeIAMUser, TypeIAMAccessKey, store.RelContains},
	)
	registerResolver(
		resolveMFADeviceToUser,
		EdgeDecl{TypeIAMUser, TypeIAMVirtualMFADevice, store.RelContains},
	)
	registerResolver(
		resolveManagedPolicyAttachments,
		EdgeDecl{TypeIAMPolicy, TypeIAMRole, store.RelAttachedTo},
		EdgeDecl{TypeIAMPolicy, TypeIAMServiceLinkedRole, store.RelAttachedTo},
		EdgeDecl{TypeIAMPolicy, TypeIAMUser, store.RelAttachedTo},
		EdgeDecl{TypeIAMPolicy, TypeIAMGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveUserGroupMemberships,
		EdgeDecl{TypeIAMGroup, TypeIAMUser, store.RelContains},
	)
	registerResolver(
		resolveIAMRoleFederatedTrust,
		EdgeDecl{TypeIAMRole, TypeIAMSAMLProvider, store.RelAssumes},
		EdgeDecl{TypeIAMRole, TypeIAMOIDCProvider, store.RelAssumes},
		EdgeDecl{TypeIAMServiceLinkedRole, TypeIAMSAMLProvider, store.RelAssumes},
		EdgeDecl{TypeIAMServiceLinkedRole, TypeIAMOIDCProvider, store.RelAssumes},
	)
	registerResolver(
		resolveIAMPolicyResources,
		// Each source policy type emits `uses` to every classifyPolicyResource
		// target type. Sources: managed + role/user/group inline. Targets:
		// KMS, S3, Secrets, DynamoDB, Lambda, Logs, SNS, SQS, SSM, Kinesis,
		// ECR, IAM Role, IAM SLR, RDS instance/cluster, SFN, EventBridge,
		// EFS.
		EdgeDecl{TypeIAMPolicy, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeSecretsManagerSecret, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeDynamoDBTable, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeLogsLogGroup, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeSSMParameter, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeECRRepository, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeIAMServiceLinkedRole, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeRDSDBInstance, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeRDSDBCluster, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeSFNStateMachine, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeEventsEventBus, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeEventsRule, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeEFSFileSystem, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeSecretsManagerSecret, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeDynamoDBTable, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeLogsLogGroup, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeSSMParameter, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeECRRepository, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeIAMServiceLinkedRole, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeRDSDBInstance, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeRDSDBCluster, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeSFNStateMachine, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeEventsEventBus, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeEventsRule, store.RelUses},
		EdgeDecl{TypeIAMRolePolicy, TypeEFSFileSystem, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeSecretsManagerSecret, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeDynamoDBTable, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeLogsLogGroup, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeSSMParameter, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeECRRepository, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeIAMServiceLinkedRole, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeRDSDBInstance, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeRDSDBCluster, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeSFNStateMachine, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeEventsEventBus, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeEventsRule, store.RelUses},
		EdgeDecl{TypeIAMUserPolicy, TypeEFSFileSystem, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeSecretsManagerSecret, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeDynamoDBTable, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeLogsLogGroup, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeSSMParameter, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeECRRepository, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeIAMServiceLinkedRole, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeRDSDBInstance, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeRDSDBCluster, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeSFNStateMachine, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeEventsEventBus, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeEventsRule, store.RelUses},
		EdgeDecl{TypeIAMGroupPolicy, TypeEFSFileSystem, store.RelUses},
	)
	registerResolver(
		resolveIAMRoleCrossAccountTrust,
		EdgeDecl{TypeIAMRole, TypeIAMForeignAccount, store.RelCrossAccountTrust},
		EdgeDecl{TypeIAMServiceLinkedRole, TypeIAMForeignAccount, store.RelCrossAccountTrust},
	)
	registerResolver(
		resolveIAMPermissionBoundaries,
		EdgeDecl{TypeIAMRole, TypeIAMPolicy, store.RelBoundedBy},
		EdgeDecl{TypeIAMUser, TypeIAMPolicy, store.RelBoundedBy},
	)
	// Synthetic stub for cross-account trust principals whose owning account
	// is out of scan scope (R5). Pure disco bookkeeping — no upstream
	// CloudFormation resource type.
	registerExtraEmits(
		coverage.TypeDecl{Service: "iam", DiscoType: TypeIAMForeignAccount, Synthetic: true, Leaf: true},
	)
}

// resolveInstanceProfileRoles links each instance profile to the role it contains.
// The role ARN is embedded in the instance profile's stored attributes.
func resolveInstanceProfileRoles(acct *account, st *store.Store) error {
	profiles, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMInstanceProfile},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range profiles {
		var attrs struct {
			Roles []struct {
				Arn *string `json:"Arn"`
			} `json:"Roles"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || len(attrs.Roles) == 0 || attrs.Roles[0].Arn == nil {
			continue
		}
		roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, *attrs.Roles[0].Arn)
		if err := st.UpsertRelationship(roleID, r.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert role→instance-profile: %w", err)
		}
	}
	return nil
}

// resolveInlinePolicyParents links inline policies (role/user/group) to their
// parent principal. NativeID encodes the parent ARN as "{parentARN}/policy/{name}".
// For role policies, both TypeIAMRole and TypeIAMServiceLinkedRole are tried so
// that service-linked role inline policies resolve correctly.
func resolveInlinePolicyParents(acct *account, st *store.Store) error {
	// Role policies: parent may be a regular role or a service-linked role.
	rolePolicies, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMRolePolicy},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rolePolicies {
		idx := strings.Index(r.NativeID, "/policy/")
		if idx < 0 {
			continue
		}
		parentARN := r.NativeID[:idx]
		// Determine role type from ARN: service-linked roles have /aws-service-role/ in the path.
		pt := TypeIAMRole
		if strings.Contains(parentARN, "/aws-service-role/") {
			pt = TypeIAMServiceLinkedRole
		}
		parentID := store.ResourceID("aws", acct.ID, pt, parentARN)
		if err := st.UpsertRelationship(parentID, r.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert role-policy parent: %w", err)
		}
	}

	// User and group policies: single parent type each.
	type entry struct {
		policyType string
		parentType string
	}
	for _, e := range []entry{
		{TypeIAMUserPolicy, TypeIAMUser},
		{TypeIAMGroupPolicy, TypeIAMGroup},
	} {
		policies, err := st.ListResources(store.ResourceFilter{
			Provider:  "aws",
			AccountID: acct.ID,
			Types:     []string{e.policyType},
			Limit:     util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range policies {
			idx := strings.Index(r.NativeID, "/policy/")
			if idx < 0 {
				continue
			}
			parentID := store.ResourceID("aws", acct.ID, e.parentType, r.NativeID[:idx])
			if err := st.UpsertRelationship(parentID, r.ID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert %s parent: %w", e.policyType, err)
			}
		}
	}
	return nil
}

// resolveAccessKeyUsers links each access key to its owning user.
// NativeID encodes the user ARN as "{userARN}/access-key/{keyID}".
func resolveAccessKeyUsers(acct *account, st *store.Store) error {
	keys, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMAccessKey},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range keys {
		idx := strings.Index(r.NativeID, "/access-key/")
		if idx < 0 {
			continue
		}
		userID := store.ResourceID("aws", acct.ID, TypeIAMUser, r.NativeID[:idx])
		if err := st.UpsertRelationship(userID, r.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert user→access-key: %w", err)
		}
	}
	return nil
}

// resolveMFADeviceToUser links each assigned virtual MFA device to its owning
// user. The owning user's ARN is stored in the device's attributes JSON under
// the User.Arn field, present only when the device is assigned to a user.
func resolveMFADeviceToUser(acct *account, st *store.Store) error {
	devices, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMVirtualMFADevice},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	// FK-safe: device User.Arn may point to the AWS account root user
	// (arn:aws:iam::{acct}:root), which is not an aws:iam:user resource
	// — root has no IAM-user identity. Build the scanned-user set so we
	// skip emit when target is absent, regardless of arn-shape mismatch.
	users, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMUser},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	userByID := make(map[string]struct{}, len(users))
	for _, u := range users {
		userByID[u.ID] = struct{}{}
	}

	var attrs struct {
		User *struct {
			Arn *string `json:"Arn"`
		} `json:"User"`
	}

	for _, r := range devices {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.User == nil || attrs.User.Arn == nil {
			continue // unassigned device
		}
		userID := store.ResourceID("aws", acct.ID, TypeIAMUser, *attrs.User.Arn)
		if _, ok := userByID[userID]; !ok {
			continue // root or unscanned user — no IAM-user resource to link to
		}
		if err := st.UpsertRelationship(userID, r.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert user→mfaDevice: %w", err)
		}
	}
	return nil
}

// resolveManagedPolicyAttachments creates attached-to edges from each managed
// policy (customer-managed AND AWS-managed) to the roles, users, and groups it is
// attached to. GAAD already returns every principal's AttachedManagedPolicies with
// ARNs (stored verbatim in the principal's attributes by scanIAMAuthDetails), so we
// read attachments straight from the store — no per-policy ListEntitiesForPolicy
// fan-out, and IncludeManaged surfaces the AWS-managed catalogue rows that the
// default filter would hide (so AdministratorAccess et al. become edge targets).
func resolveManagedPolicyAttachments(acct *account, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Provider:       "aws",
		AccountID:      acct.ID,
		Types:          []string{TypeIAMPolicy},
		IncludeManaged: true,
		Limit:          util.AllResources,
	})
	if err != nil {
		return err
	}
	policyByARN := make(map[string]string, len(policies))
	for _, p := range policies {
		policyByARN[p.NativeID] = p.ID
	}
	if len(policyByARN) == 0 {
		return nil
	}

	// SLRs carry ManagedByProvider=true, so IncludeManaged is required or they drop
	// out of the principal set entirely.
	principals, err := st.ListResources(store.ResourceFilter{
		Provider:       "aws",
		AccountID:      acct.ID,
		Types:          []string{TypeIAMRole, TypeIAMServiceLinkedRole, TypeIAMUser, TypeIAMGroup},
		IncludeManaged: true,
		Limit:          util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, pr := range principals {
		if pr.AttributesJSON == "" {
			continue
		}
		var detail struct {
			AttachedManagedPolicies []struct {
				PolicyArn string `json:"PolicyArn"`
			} `json:"AttachedManagedPolicies"`
		}
		if err := json.Unmarshal([]byte(pr.AttributesJSON), &detail); err != nil {
			continue
		}
		for _, amp := range detail.AttachedManagedPolicies {
			policyID, ok := policyByARN[amp.PolicyArn]
			if !ok {
				continue // policy not in store — FK-safe skip
			}
			if err := st.UpsertRelationship(policyID, pr.ID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert managed-policy→principal: %w", err)
			}
		}
	}
	return nil
}

// principalList decodes the Principal.Federated field of a trust policy,
// which AWS serializes as either a single string or an array of strings.
type principalList []string

func (p *principalList) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '[' {
		var arr []string
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		*p = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*p = []string{s}
	return nil
}

// resolveIAMRoleFederatedTrust emits assumes edges from a role to any SAML
// or OIDC identity provider named in its trust policy's Federated principals.
// AssumeRolePolicyDocument is URL-encoded by the AWS SDK; decode before parsing.
func resolveIAMRoleFederatedTrust(acct *account, st *store.Store) error {
	roles, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMRole, TypeIAMServiceLinkedRole},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range roles {
		var attrs struct {
			AssumeRolePolicyDocument *string `json:"AssumeRolePolicyDocument"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AssumeRolePolicyDocument == nil || *attrs.AssumeRolePolicyDocument == "" {
			continue
		}
		doc, err := url.QueryUnescape(*attrs.AssumeRolePolicyDocument)
		if err != nil {
			continue
		}
		var policy struct {
			Statement []struct {
				Principal struct {
					Federated principalList `json:"Federated"`
				} `json:"Principal"`
			} `json:"Statement"`
		}
		if err := json.Unmarshal([]byte(doc), &policy); err != nil {
			continue
		}
		for _, stmt := range policy.Statement {
			for _, arn := range stmt.Principal.Federated {
				var providerType string
				switch {
				case strings.Contains(arn, ":saml-provider/"):
					providerType = TypeIAMSAMLProvider
				case strings.Contains(arn, ":oidc-provider/"):
					providerType = TypeIAMOIDCProvider
				default:
					continue
				}
				targetID := store.ResourceID("aws", acct.ID, providerType, arn)
				if err := st.UpsertRelationship(r.ID, targetID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert role→federated-provider: %w", err)
				}
			}
		}
	}
	return nil
}

// statementList decodes a policy doc Statement field, which AWS allows as
// either a single object or an array of objects.
type statementList []policyStmt

func (s *statementList) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '[' {
		var arr []policyStmt
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		*s = arr
		return nil
	}
	var one policyStmt
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*s = []policyStmt{one}
	return nil
}

// resourceList decodes a Statement.Resource field, single string or array.
type resourceList []string

func (r *resourceList) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '[' {
		var arr []string
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		*r = arr
		return nil
	}
	var one string
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*r = []string{one}
	return nil
}

type policyStmt struct {
	Effect   string       `json:"Effect"`
	Resource resourceList `json:"Resource"`
}

// resolveIAMPolicyResources walks the Document of every IAM policy resource
// (managed + inline role/user/group) and emits "uses" edges from the policy to
// each scanned KMS key, S3 bucket, Secrets Manager secret, and DynamoDB table
// referenced by an Allow statement. Cross-account, wildcard, and unscanned
// targets are skipped FK-safe.
func resolveIAMPolicyResources(acct *account, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Provider:       "aws",
		AccountID:      acct.ID,
		Types:          []string{TypeIAMPolicy, TypeIAMRolePolicy, TypeIAMUserPolicy, TypeIAMGroupPolicy},
		IncludeManaged: true, // AWS-managed policy documents carry resource refs too
		Limit:          util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}

	sets, err := loadPolicyResourceSets(acct, st)
	if err != nil {
		return err
	}

	for _, p := range policies {
		doc := extractPolicyDoc(p)
		if doc == "" {
			continue
		}
		decoded, err := url.QueryUnescape(doc)
		if err != nil {
			continue
		}
		var parsed struct {
			Statement statementList `json:"Statement"`
		}
		if err := json.Unmarshal([]byte(decoded), &parsed); err != nil {
			continue
		}
		// Region carried by the policy itself only matters as a fallback for KMS
		// references that lack a region (bare key UUID, alias name); managed and
		// inline policies are global, so use the empty string and let any embedded
		// ARN provide its own region.
		region := ""
		if p.Region != nil {
			region = *p.Region
		}
		for _, stmt := range parsed.Statement {
			if !strings.EqualFold(stmt.Effect, "Allow") {
				continue
			}
			for _, ref := range stmt.Resource {
				if ref == "" || ref == "*" {
					continue
				}
				targetID, ok := classifyPolicyResource(ref, region, acct.ID, sets)
				if !ok {
					continue
				}
				if err := st.UpsertRelationship(p.ID, targetID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert iam-policy→resource: %w", err)
				}
			}
		}
	}
	return nil
}

// extractPolicyDoc returns the URL-encoded policy document for any of the four
// IAM policy types. Inline Get*Policy responses carry it under "PolicyDocument";
// managed policies (wrapped at scan time) carry it under "PolicyVersion.Document".
func extractPolicyDoc(r store.Resource) string {
	switch r.Type {
	case TypeIAMPolicy:
		var w struct {
			PolicyVersion *struct {
				Document *string `json:"Document"`
			} `json:"PolicyVersion"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &w); err != nil || w.PolicyVersion == nil || w.PolicyVersion.Document == nil {
			return ""
		}
		return *w.PolicyVersion.Document
	default:
		var w struct {
			PolicyDocument *string `json:"PolicyDocument"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &w); err != nil || w.PolicyDocument == nil {
			return ""
		}
		return *w.PolicyDocument
	}
}

// policyResourceSets bundles the per-type FK-safety lookups built once per
// resolver run. Each id-set maps a stable resource ID to membership; the
// classifier returns ok=true only when the computed id appears in the
// matching set, so cross-account / unscanned targets skip silently.
type policyResourceSets struct {
	kms           *kmsResolveIndex
	buckets       map[string]struct{}
	secrets       map[string]struct{}
	tables        map[string]struct{}
	lambdas       map[string]struct{}
	logGroups     map[string]struct{}
	topics        map[string]struct{}
	queues        map[string]struct{}
	parameters    map[string]struct{}
	streams       map[string]struct{}
	repositories  map[string]struct{}
	roles         map[string]struct{}
	serviceLinked map[string]struct{}
	rdsInstances  map[string]struct{}
	rdsClusters   map[string]struct{}
	stateMachines map[string]struct{}
	eventBuses    map[string]struct{}
	eventRules    map[string]struct{}
	efsFS         map[string]struct{}
}

// loadPolicyResourceSets constructs policyResourceSets for one account in a
// single pass over the store.
func loadPolicyResourceSets(acct *account, st *store.Store) (*policyResourceSets, error) {
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return nil, err
	}
	s := &policyResourceSets{kms: kmsIdx}
	for _, pair := range []struct {
		dst   *map[string]struct{}
		rtype string
	}{
		{&s.buckets, TypeS3Bucket},
		{&s.secrets, TypeSecretsManagerSecret},
		{&s.tables, TypeDynamoDBTable},
		{&s.lambdas, TypeLambdaFunction},
		{&s.logGroups, TypeLogsLogGroup},
		{&s.topics, TypeSNSTopic},
		{&s.queues, TypeSQSQueue},
		{&s.parameters, TypeSSMParameter},
		{&s.streams, TypeKinesisStream},
		{&s.repositories, TypeECRRepository},
		{&s.roles, TypeIAMRole},
		{&s.serviceLinked, TypeIAMServiceLinkedRole},
		{&s.rdsInstances, TypeRDSDBInstance},
		{&s.rdsClusters, TypeRDSDBCluster},
		{&s.stateMachines, TypeSFNStateMachine},
		{&s.eventBuses, TypeEventsEventBus},
		{&s.eventRules, TypeEventsRule},
		{&s.efsFS, TypeEFSFileSystem},
	} {
		set, err := resourceIDSet(st, acct.ID, pair.rtype)
		if err != nil {
			return nil, err
		}
		*pair.dst = set
	}
	return s, nil
}

// lookupTargetID returns the stored resource ID for ref under rtype, gated on
// wildcard-free input and presence in the per-type id set. Shared by the
// straightforward classifyPolicyResource branches that just need a wildcard
// guard + map lookup.
func lookupTargetID(ref, rtype, acctID string, set map[string]struct{}) (string, bool) {
	if strings.ContainsAny(ref, "*?") {
		return "", false
	}
	id := store.ResourceID("aws", acctID, rtype, ref)
	if _, ok := set[id]; ok {
		return id, true
	}
	return "", false
}

// policyResourceClassifier matches a Resource ARN's service+kind and returns
// the stored resource ID via the per-type id set bundle. Returns ok=false
// to signal "not this classifier" — caller falls through to the next entry.
type policyResourceClassifier struct {
	match    func(ref string) bool
	classify func(ref, region, acctID string, sets *policyResourceSets) (string, bool)
}

// policyResourceClassifiers is the ordered dispatch table consulted by
// classifyPolicyResource. Order matters where prefixes overlap (e.g.
// `:event-bus/` vs `:rule/` under `arn:aws:events:`).
var policyResourceClassifiers = []policyResourceClassifier{
	{
		match:    func(ref string) bool { return strings.Contains(ref, ":kms:") },
		classify: classifyKMSPolicyResource,
	},
	{
		match: func(ref string) bool { return strings.HasPrefix(ref, "arn:aws:s3:::") },
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return classifyS3Resource(ref, acctID, sets)
		},
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:secretsmanager:") && strings.Contains(ref, ":secret:")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return classifySecretResource(ref, acctID, sets)
		},
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:dynamodb:") && strings.Contains(ref, ":table/")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return classifyDynamoDBResource(ref, acctID, sets)
		},
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:lambda:") && strings.Contains(ref, ":function:")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return classifyLambdaResource(ref, acctID, sets)
		},
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:logs:") && strings.Contains(ref, ":log-group:")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return classifyLogGroupResource(ref, acctID, sets)
		},
	},
	{
		match:    func(ref string) bool { return strings.HasPrefix(ref, "arn:aws:sns:") },
		classify: classifySNSPolicyResource,
	},
	{
		match:    func(ref string) bool { return strings.HasPrefix(ref, "arn:aws:sqs:") },
		classify: classifySQSPolicyResource,
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:ssm:") && strings.Contains(ref, ":parameter")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return lookupTargetID(ref, TypeSSMParameter, acctID, sets.parameters)
		},
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:kinesis:") && strings.Contains(ref, ":stream/")
		},
		classify: classifyKinesisPolicyResource,
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:ecr:") && strings.Contains(ref, ":repository/")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return lookupTargetID(ref, TypeECRRepository, acctID, sets.repositories)
		},
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:iam:") && strings.Contains(ref, ":role/")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return classifyIAMRoleResource(ref, acctID, sets)
		},
	},
	{
		match: func(ref string) bool { return strings.HasPrefix(ref, "arn:aws:rds:") },
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return classifyRDSResource(ref, acctID, sets)
		},
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:states:") && strings.Contains(ref, ":stateMachine:")
		},
		classify: classifyStatesPolicyResource,
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:events:") && strings.Contains(ref, ":event-bus/")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return lookupTargetID(ref, TypeEventsEventBus, acctID, sets.eventBuses)
		},
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:events:") && strings.Contains(ref, ":rule/")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return lookupTargetID(ref, TypeEventsRule, acctID, sets.eventRules)
		},
	},
	{
		match: func(ref string) bool {
			return strings.HasPrefix(ref, "arn:aws:elasticfilesystem:") && strings.Contains(ref, ":file-system/")
		},
		classify: func(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
			return lookupTargetID(ref, TypeEFSFileSystem, acctID, sets.efsFS)
		},
	},
}

// classifyPolicyResource maps a Resource ARN to a stored resource ID via the
// per-type id sets. Returns ok=false for unrecognized services, wildcard ARNs,
// and cross-account / unscanned targets — the caller emits no edge.
func classifyPolicyResource(ref, region, acctID string, sets *policyResourceSets) (string, bool) {
	for _, c := range policyResourceClassifiers {
		if c.match(ref) {
			return c.classify(ref, region, acctID, sets)
		}
	}
	return "", false
}

// classifyKMSPolicyResource handles `arn:aws:kms:` refs. KMS grants don't
// use object-suffix patterns; any wildcard here means the policy targets
// a class of keys rather than one identifier.
func classifyKMSPolicyResource(ref, region, acctID string, sets *policyResourceSets) (string, bool) {
	if strings.ContainsAny(ref, "*?") {
		return "", false
	}
	return sets.kms.resolveKMSKeyID(ref, region, acctID)
}

// classifySNSPolicyResource handles topic ARNs only. Subscription ARNs add a
// 6th colon segment past the topic name; reject those (no scanned type for
// subscriptions today).
func classifySNSPolicyResource(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
	if strings.Count(ref, ":") != 5 {
		return "", false
	}
	return lookupTargetID(ref, TypeSNSTopic, acctID, sets.topics)
}

func classifySQSPolicyResource(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
	if strings.Count(ref, ":") != 5 {
		return "", false
	}
	return lookupTargetID(ref, TypeSQSQueue, acctID, sets.queues)
}

func classifyKinesisPolicyResource(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
	_, after, _ := strings.Cut(ref, ":stream/")
	if strings.Contains(after, "/") {
		return "", false
	}
	return lookupTargetID(ref, TypeKinesisStream, acctID, sets.streams)
}

// classifyStatesPolicyResource rejects Step Functions service-integration
// ARNs (`arn:aws:states:::lambda:invoke` etc.) which carry empty region+
// account so they don't hash to phantom state machines (aws/CLAUDE.md
// "AWS service-integration ARNs use :::").
func classifyStatesPolicyResource(ref, _, acctID string, sets *policyResourceSets) (string, bool) {
	if strings.Contains(ref, ":::") {
		return "", false
	}
	return lookupTargetID(ref, TypeSFNStateMachine, acctID, sets.stateMachines)
}

// classifyS3Resource trims object-key suffix to the bucket ARN before lookup.
// "bucket/*" means "all objects in this bucket" — still concrete bucket grant.
func classifyS3Resource(ref, acctID string, sets *policyResourceSets) (string, bool) {
	bucketARN := ref
	if i := strings.Index(ref[len("arn:aws:s3:::"):], "/"); i >= 0 {
		bucketARN = ref[:len("arn:aws:s3:::")+i]
	}
	return lookupTargetID(bucketARN, TypeS3Bucket, acctID, sets.buckets)
}

// classifySecretResource trims version-stage / version-id tail (keep first 7
// colon segments) before lookup.
func classifySecretResource(ref, acctID string, sets *policyResourceSets) (string, bool) {
	parts := strings.SplitN(ref, ":", 8)
	if len(parts) < 7 {
		return "", false
	}
	return lookupTargetID(strings.Join(parts[:7], ":"), TypeSecretsManagerSecret, acctID, sets.secrets)
}

// classifyDynamoDBResource trims table-child suffixes (/index/..., /stream/...,
// /backup/..., /export/...) before lookup.
func classifyDynamoDBResource(ref, acctID string, sets *policyResourceSets) (string, bool) {
	tableARN := ref
	base := strings.Index(ref, ":table/")
	if base >= 0 {
		rest := ref[base+len(":table/"):]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			tableARN = ref[:base+len(":table/")+slash]
		}
	}
	return lookupTargetID(tableARN, TypeDynamoDBTable, acctID, sets.tables)
}

// classifyLambdaResource trims qualifier suffix (`:VERSION|ALIAS`) — scanner
// stores the unqualified ARN (first 7 colon segments).
func classifyLambdaResource(ref, acctID string, sets *policyResourceSets) (string, bool) {
	parts := strings.SplitN(ref, ":", 8)
	if len(parts) < 7 {
		return "", false
	}
	return lookupTargetID(strings.Join(parts[:7], ":"), TypeLambdaFunction, acctID, sets.lambdas)
}

// classifyLogGroupResource trims `:*` and `:log-stream:...` tails — scanner
// NativeID stops at the group name.
func classifyLogGroupResource(ref, acctID string, sets *policyResourceSets) (string, bool) {
	base := strings.Index(ref, ":log-group:")
	nameStart := base + len(":log-group:")
	nameEnd := len(ref)
	if i := strings.Index(ref[nameStart:], ":"); i >= 0 {
		nameEnd = nameStart + i
	}
	return lookupTargetID(ref[:nameEnd], TypeLogsLogGroup, acctID, sets.logGroups)
}

// classifyIAMRoleResource splits service-linked roles (under
// `/aws-service-role/`) into their own scanned type.
func classifyIAMRoleResource(ref, acctID string, sets *policyResourceSets) (string, bool) {
	rtype := TypeIAMRole
	bag := sets.roles
	if strings.Contains(ref, "/aws-service-role/") {
		rtype = TypeIAMServiceLinkedRole
		bag = sets.serviceLinked
	}
	return lookupTargetID(ref, rtype, acctID, bag)
}

// classifyRDSResource matches only `:db:` and `:cluster:` segments — snapshot,
// parameter-group, subnet-group share the prefix but live under different
// scanned types.
func classifyRDSResource(ref, acctID string, sets *policyResourceSets) (string, bool) {
	switch {
	case strings.Contains(ref, ":db:"):
		return lookupTargetID(ref, TypeRDSDBInstance, acctID, sets.rdsInstances)
	case strings.Contains(ref, ":cluster:"):
		return lookupTargetID(ref, TypeRDSDBCluster, acctID, sets.rdsClusters)
	}
	return "", false
}

// resourceIDSet returns the stable IDs of every resource of rtype for one
// account. Used by resolvers that need FK-safe per-type membership lookup.
func resourceIDSet(st *store.Store, accountID, rtype string) (map[string]struct{}, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: accountID,
		Types:     []string{rtype},
		Limit:     util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		out[r.ID] = struct{}{}
	}
	return out, nil
}

// resolveUserGroupMemberships calls ListGroupsForUser for each user and creates
// contains edges from each group to the user, modelling AWS::IAM::UserToGroupAddition.
func resolveUserGroupMemberships(acct *account, st *store.Store) error {
	users, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMUser},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	client := iam.NewFromConfig(acct.cfg)
	sem := semaphore.NewWeighted(fanoutHigh)
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(context.Background())
	for _, u := range users {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := iam.NewListGroupsForUserPaginator(client, &iam.ListGroupsForUserInput{
				UserName: u.Name,
			})
			for pager.HasMorePages() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("iam:ListGroupsForUser %s: %w", sv(u.Name), err)
				}
				mu.Lock()
				for _, group := range page.Groups {
					if group.Arn == nil {
						continue
					}
					groupID := store.ResourceID("aws", acct.ID, TypeIAMGroup, *group.Arn)
					if err := st.UpsertRelationship(groupID, u.ID, store.RelContains, "directed", nil); err != nil {
						mu.Unlock()
						return fmt.Errorf("upsert group→user membership: %w", err)
					}
				}
				mu.Unlock()
			}
			return nil
		})
	}
	return g.Wait()
}

// resolveIAMRoleCrossAccountTrust walks each role's AssumeRolePolicyDocument and
// emits cross-account-trust edges for any Allow Statement Principal.AWS that
// names a different AWS account. Foreign accounts are not in scan scope, so
// we synthesize one aws:iam:foreign-account stub per distinct foreign account
// so the FK on relationships.to_id holds and the foreign account is visible
// as a graph node. ROADMAP R5.
func resolveIAMRoleCrossAccountTrust(acct *account, st *store.Store) error {
	roles, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMRole, TypeIAMServiceLinkedRole},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		return nil
	}
	scanID := roles[0].DiscoveredBy

	type pending struct {
		fromID    string
		principal string
		acctID    string
	}
	var edges []pending
	stubs := map[string]struct{}{}
	for _, r := range roles {
		var attrs struct {
			AssumeRolePolicyDocument *string `json:"AssumeRolePolicyDocument"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AssumeRolePolicyDocument == nil || *attrs.AssumeRolePolicyDocument == "" {
			continue
		}
		doc, err := url.QueryUnescape(*attrs.AssumeRolePolicyDocument)
		if err != nil {
			continue
		}
		var parsed struct {
			Statement statementPrincipalList `json:"Statement"`
		}
		if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
			continue
		}
		for _, stmt := range parsed.Statement {
			if !strings.EqualFold(stmt.Effect, "Allow") {
				continue
			}
			for _, p := range stmt.Principal.AWS {
				other, ok := foreignAccountFromPrincipal(p, acct.ID)
				if !ok {
					continue
				}
				stubs[other] = struct{}{}
				edges = append(edges, pending{fromID: r.ID, principal: p, acctID: other})
			}
		}
	}

	if len(edges) == 0 {
		return nil
	}

	stubResources := make([]*store.Resource, 0, len(stubs))
	for other := range stubs {
		nativeID := fmt.Sprintf("arn:aws:iam::%s:root", other)
		name := other
		stubResources = append(stubResources, &store.Resource{
			Provider:       "aws",
			AccountID:      other,
			Type:           TypeIAMForeignAccount,
			NativeID:       nativeID,
			Name:           &name,
			Region:         regionGlobal,
			AttributesJSON: fmt.Sprintf(`{"AccountId":%q,"Synthetic":true}`, other),
			DiscoveredBy:   scanID,
		})
	}
	if _, err := st.UpsertResources(stubResources); err != nil {
		return fmt.Errorf("upsert foreign-account stubs: %w", err)
	}

	for _, e := range edges {
		toID := store.ResourceID("aws", e.acctID, TypeIAMForeignAccount, fmt.Sprintf("arn:aws:iam::%s:root", e.acctID))
		attrs := mustJSON(map[string]string{"principal": e.principal, "trust-account": e.acctID})
		if err := st.UpsertRelationship(e.fromID, toID, store.RelCrossAccountTrust, "directed", &attrs); err != nil {
			return fmt.Errorf("upsert cross-account-trust: %w", err)
		}
	}
	return nil
}

// statementPrincipalList decodes Statement[] with Principal.AWS payload.
// Mirrors statementList shape (string-or-array) but preserves the Principal
// field that the policy-resource walker discards.
type statementPrincipalList []principalStatement

func (s *statementPrincipalList) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '[' {
		var arr []principalStatement
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		*s = arr
		return nil
	}
	var one principalStatement
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*s = []principalStatement{one}
	return nil
}

type principalStatement struct {
	Effect    string `json:"Effect"`
	Principal struct {
		AWS principalList `json:"AWS"`
	} `json:"Principal"`
}

// foreignAccountFromPrincipal extracts the account ID from a Principal.AWS entry
// when it refers to an account other than self. Returns ok=true for:
//   - bare 12-digit account IDs ("123456789012")
//   - ARNs of form arn:aws:iam::<acct>:root | user/* | role/*
//
// Wildcards, self-account refs, and malformed inputs return ok=false.
func foreignAccountFromPrincipal(p, selfAcct string) (string, bool) {
	if p == "" || p == "*" {
		return "", false
	}
	if len(p) == 12 && isAllDigits(p) {
		if p == selfAcct {
			return "", false
		}
		return p, true
	}
	parts := strings.SplitN(p, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" || parts[2] != "iam" {
		return "", false
	}
	other := parts[4]
	if len(other) != 12 || !isAllDigits(other) || other == selfAcct {
		return "", false
	}
	return other, true
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// resolveIAMPermissionBoundaries emits `bounded-by` edges from each IAM role
// or user to its permission-boundary policy. AWS SDK serializes the boundary
// as `{"PermissionsBoundary": {"PermissionsBoundaryArn": "...",
// "PermissionsBoundaryType": "Policy"}}` on the principal's stored attrs.
//
// FK-safe: builds an in-account `TypeIAMPolicy` ID set once, skips emit when
// the boundary's policy is not in scan scope (cross-account boundary, or
// account hasn't scanned policies). Cross-account boundary ARNs short-circuit
// because the rebuilt ResourceID uses our account, never the foreign one.
func resolveIAMPermissionBoundaries(acct *account, st *store.Store) error {
	principals, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMRole, TypeIAMUser},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(principals) == 0 {
		return nil
	}

	policies, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMPolicy},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	policyIDSet := make(map[string]struct{}, len(policies))
	for _, p := range policies {
		policyIDSet[p.ID] = struct{}{}
	}

	for _, r := range principals {
		var attrs struct {
			PermissionsBoundary *struct {
				PermissionsBoundaryArn  *string `json:"PermissionsBoundaryArn"`
				PermissionsBoundaryType *string `json:"PermissionsBoundaryType"`
			} `json:"PermissionsBoundary"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PermissionsBoundary == nil || sv(attrs.PermissionsBoundary.PermissionsBoundaryArn) == "" {
			continue
		}
		boundaryARN := sv(attrs.PermissionsBoundary.PermissionsBoundaryArn)
		toID := store.ResourceID("aws", acct.ID, TypeIAMPolicy, boundaryARN)
		if _, ok := policyIDSet[toID]; !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, toID, store.RelBoundedBy, "directed", nil); err != nil {
			return fmt.Errorf("upsert principal→boundary: %w", err)
		}
	}
	return nil
}
