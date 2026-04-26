package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerResolver(resolveInstanceProfileRoles)
	registerResolver(resolveInlinePolicyParents)
	registerResolver(resolveAccessKeyUsers)
	registerResolver(resolveMFADeviceToUser)
	registerResolver(resolveManagedPolicyAttachments)
	registerResolver(resolveUserGroupMemberships)
	registerResolver(resolveIAMRoleFederatedTrust)
	registerResolver(resolveIAMPolicyResources)
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
		if err := st.UpsertRelationship(r.ID, roleID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert instance-profile→role: %w", err)
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

// resolveManagedPolicyAttachments calls ListEntitiesForPolicy for each customer-
// managed policy and creates attached-to edges to roles, users, and groups.
// The IAM SDK returns only names (not ARNs) from ListEntitiesForPolicy, so we
// pre-index all principals by name to look up their stable resource IDs.
func resolveManagedPolicyAttachments(acct *account, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMPolicy},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}

	// Build name→resourceID indexes for each principal type.
	rolesByName, err := resourceIDsByName(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}
	slrByName, err := resourceIDsByName(st, acct.ID, TypeIAMServiceLinkedRole)
	if err != nil {
		return err
	}
	usersByName, err := resourceIDsByName(st, acct.ID, TypeIAMUser)
	if err != nil {
		return err
	}
	groupsByName, err := resourceIDsByName(st, acct.ID, TypeIAMGroup)
	if err != nil {
		return err
	}

	// Fan out across all policies concurrently; collect relationship edges and
	// write them under a mutex to avoid concurrent SQLite writes.
	client := iam.NewFromConfig(acct.cfg)
	sem := semaphore.NewWeighted(fanoutMed)
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(context.Background())
	for _, r := range policies {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := iam.NewListEntitiesForPolicyPaginator(client, &iam.ListEntitiesForPolicyInput{
				PolicyArn: &r.NativeID,
			})
			for pager.HasMorePages() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("iam:ListEntitiesForPolicy %s: %w", r.NativeID, err)
				}
				mu.Lock()
				for _, role := range page.PolicyRoles {
					name := sv(role.RoleName)
					// Check both role maps; only upsert if the resource was actually discovered.
					for _, nameMap := range []map[string]string{rolesByName, slrByName} {
						if tID, ok := nameMap[name]; ok {
							if err := st.UpsertRelationship(r.ID, tID, store.RelAttachedTo, "directed", nil); err != nil {
								mu.Unlock()
								return fmt.Errorf("upsert managed-policy→role: %w", err)
							}
						}
					}
				}
				for _, user := range page.PolicyUsers {
					name := sv(user.UserName)
					if userID, ok := usersByName[name]; ok {
						if err := st.UpsertRelationship(r.ID, userID, store.RelAttachedTo, "directed", nil); err != nil {
							mu.Unlock()
							return fmt.Errorf("upsert managed-policy→user: %w", err)
						}
					}
				}
				for _, group := range page.PolicyGroups {
					name := sv(group.GroupName)
					if groupID, ok := groupsByName[name]; ok {
						if err := st.UpsertRelationship(r.ID, groupID, store.RelAttachedTo, "directed", nil); err != nil {
							mu.Unlock()
							return fmt.Errorf("upsert managed-policy→group: %w", err)
						}
					}
				}
				mu.Unlock()
			}
			return nil
		})
	}
	return g.Wait()
}

// resourceIDsByName loads all resources of rtype for the account and returns a
// map of name → stable resource ID. Used to resolve name-only API responses.
func resourceIDsByName(st *store.Store, accountID, rtype string) (map[string]string, error) {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: accountID,
		Types:     []string{rtype},
		Limit:     util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(resources))
	for _, r := range resources {
		if r.Name != nil {
			m[*r.Name] = r.ID
		}
	}
	return m, nil
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
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMPolicy, TypeIAMRolePolicy, TypeIAMUserPolicy, TypeIAMGroupPolicy},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}

	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	bucketIDs, err := resourceIDSet(st, acct.ID, TypeS3Bucket)
	if err != nil {
		return err
	}
	secretIDs, err := resourceIDSet(st, acct.ID, TypeSecretsManagerSecret)
	if err != nil {
		return err
	}
	tableIDs, err := resourceIDSet(st, acct.ID, TypeDynamoDBTable)
	if err != nil {
		return err
	}
	lambdaIDs, err := resourceIDSet(st, acct.ID, TypeLambdaFunction)
	if err != nil {
		return err
	}
	logGroupIDs, err := resourceIDSet(st, acct.ID, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	topicIDs, err := resourceIDSet(st, acct.ID, TypeSNSTopic)
	if err != nil {
		return err
	}
	queueIDs, err := resourceIDSet(st, acct.ID, TypeSQSQueue)
	if err != nil {
		return err
	}
	paramIDs, err := resourceIDSet(st, acct.ID, TypeSSMParameter)
	if err != nil {
		return err
	}
	streamIDs, err := resourceIDSet(st, acct.ID, TypeKinesisStream)
	if err != nil {
		return err
	}
	repoIDs, err := resourceIDSet(st, acct.ID, TypeECRRepository)
	if err != nil {
		return err
	}
	// IAM roles + service-linked roles share an id set; the resolver picks the
	// type for ResourceID based on the "/aws-service-role/" path segment.
	roleIDs, err := resourceIDSet(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}
	slrIDs, err := resourceIDSet(st, acct.ID, TypeIAMServiceLinkedRole)
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
				targetID, ok := classifyPolicyResource(ref, region, acct.ID, kmsIdx, bucketIDs, secretIDs, tableIDs, lambdaIDs, logGroupIDs, topicIDs, queueIDs, paramIDs, streamIDs, repoIDs, roleIDs, slrIDs)
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

// classifyPolicyResource maps a Resource ARN to a stored resource ID via the
// per-type id sets. Returns ok=false for unrecognized services, wildcard ARNs,
// and cross-account / unscanned targets — the caller emits no edge.
func classifyPolicyResource(ref, region, acctID string, kmsIdx *kmsResolveIndex, bucketIDs, secretIDs, tableIDs, lambdaIDs, logGroupIDs, topicIDs, queueIDs, paramIDs, streamIDs, repoIDs, roleIDs, slrIDs map[string]struct{}) (string, bool) {
	switch {
	case strings.Contains(ref, ":kms:"):
		// KMS grants don't use object-suffix patterns; any wildcard here means
		// the policy targets a class of keys rather than one identifier.
		if strings.ContainsAny(ref, "*?") {
			return "", false
		}
		return kmsIdx.resolveKMSKeyID(ref, region, acctID)
	case strings.HasPrefix(ref, "arn:aws:s3:::"):
		// Strip object-key suffix: arn:aws:s3:::bucket/path → arn:aws:s3:::bucket.
		// "/*" after the bucket name means "all objects in this bucket" — still a
		// concrete bucket grant. A wildcard inside the bucket-name segment itself
		// (e.g. "prod-*") is a name pattern; skip.
		bucketARN := ref
		if i := strings.Index(ref[len("arn:aws:s3:::"):], "/"); i >= 0 {
			bucketARN = ref[:len("arn:aws:s3:::")+i]
		}
		if strings.ContainsAny(bucketARN, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeS3Bucket, bucketARN)
		if _, ok := bucketIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:secretsmanager:") && strings.Contains(ref, ":secret:"):
		// Trim version-stage / version-id tail: keep first 7 colon-separated segments.
		parts := strings.SplitN(ref, ":", 8)
		if len(parts) < 7 {
			return "", false
		}
		secretARN := strings.Join(parts[:7], ":")
		if strings.ContainsAny(secretARN, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeSecretsManagerSecret, secretARN)
		if _, ok := secretIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:dynamodb:") && strings.Contains(ref, ":table/"):
		// Trim child suffixes: /index/..., /stream/..., /backup/..., /export/....
		tableARN := ref
		base := strings.Index(ref, ":table/")
		if base >= 0 {
			rest := ref[base+len(":table/"):]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				tableARN = ref[:base+len(":table/")+slash]
			}
		}
		if strings.ContainsAny(tableARN, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeDynamoDBTable, tableARN)
		if _, ok := tableIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:lambda:") && strings.Contains(ref, ":function:"):
		// Lambda function ARNs: arn:aws:lambda:r:a:function:NAME[:VERSION|ALIAS].
		// Scanner stores the unqualified ARN (first 7 colon segments) as NativeID.
		parts := strings.SplitN(ref, ":", 8)
		if len(parts) < 7 {
			return "", false
		}
		fnARN := strings.Join(parts[:7], ":")
		if strings.ContainsAny(fnARN, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeLambdaFunction, fnARN)
		if _, ok := lambdaIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:logs:") && strings.Contains(ref, ":log-group:"):
		// Log group ARNs: arn:aws:logs:r:a:log-group:NAME[:*][:log-stream:...].
		// Scanner NativeID has no ":*" tail; trim everything from the first ":"
		// after the name segment.
		base := strings.Index(ref, ":log-group:")
		nameStart := base + len(":log-group:")
		nameEnd := len(ref)
		if i := strings.Index(ref[nameStart:], ":"); i >= 0 {
			nameEnd = nameStart + i
		}
		groupARN := ref[:nameEnd]
		if strings.ContainsAny(groupARN, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeLogsLogGroup, groupARN)
		if _, ok := logGroupIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:sns:"):
		// Topic ARN is whole ref. Subscription ARNs have an extra colon-separated
		// subscription-id segment past the topic name; reject those (no scanned
		// type for subscriptions today).
		if strings.Count(ref, ":") != 5 {
			return "", false
		}
		if strings.ContainsAny(ref, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeSNSTopic, ref)
		if _, ok := topicIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:sqs:"):
		// Queue ARN is whole ref (NativeID switched URL→ARN per aws/CLAUDE.md).
		if strings.Count(ref, ":") != 5 {
			return "", false
		}
		if strings.ContainsAny(ref, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeSQSQueue, ref)
		if _, ok := queueIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:ssm:") && strings.Contains(ref, ":parameter"):
		// Parameter ARN whole ref. Bare param names are skipped — policy docs
		// carry no region context, so synthesizing an ARN would silently target
		// the wrong region. Full-ARN refs are unambiguous.
		if strings.ContainsAny(ref, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeSSMParameter, ref)
		if _, ok := paramIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:kinesis:") && strings.Contains(ref, ":stream/"):
		// Stream ARNs: arn:aws:kinesis:r:a:stream/NAME. Stream consumers carry
		// an extra "/consumer/NAME:TS" tail and have no scanned type — reject.
		_, after, _ := strings.Cut(ref, ":stream/")
		if strings.Contains(after, "/") {
			return "", false
		}
		if strings.ContainsAny(ref, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeKinesisStream, ref)
		if _, ok := streamIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:ecr:") && strings.Contains(ref, ":repository/"):
		// Repository ARN whole ref. Image-tag refs would have an extra path
		// segment past the repo name; ECR doesn't surface those via Resource
		// in practice, so accept whole ref and let FK guard handle anything odd.
		if strings.ContainsAny(ref, "*?") {
			return "", false
		}
		id := store.ResourceID("aws", acctID, TypeECRRepository, ref)
		if _, ok := repoIDs[id]; ok {
			return id, true
		}
		return "", false
	case strings.HasPrefix(ref, "arn:aws:iam:") && strings.Contains(ref, ":role/"):
		// Role ARN whole ref. Service-linked roles live under "/aws-service-role/"
		// — they're a distinct resource type with its own NativeID hash.
		if strings.ContainsAny(ref, "*?") {
			return "", false
		}
		rtype := TypeIAMRole
		bag := roleIDs
		if strings.Contains(ref, "/aws-service-role/") {
			rtype = TypeIAMServiceLinkedRole
			bag = slrIDs
		}
		id := store.ResourceID("aws", acctID, rtype, ref)
		if _, ok := bag[id]; ok {
			return id, true
		}
		return "", false
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
