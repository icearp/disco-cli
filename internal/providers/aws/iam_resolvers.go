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
