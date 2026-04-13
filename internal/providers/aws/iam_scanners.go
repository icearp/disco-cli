package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:iam",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			return scanIAM(ctx, acct, st, scanID)
		},
	})
}

// scanIAM discovers all IAM resources. Phase 1 scans standalone resources in
// parallel; phase 2 scans per-principal resources that depend on phase 1 being
// in the DB first. IAM is a global service scanned once per account.
func scanIAM(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iam.NewFromConfig(acct.cfg)
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }

	// Phase 1: standalone resources — all parallel.
	g1, ctx1 := errgroup.WithContext(ctx)
	g1.Go(func() error { tt, nn, e := scanIAMRoles(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error { tt, nn, e := scanIAMUsers(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error { tt, nn, e := scanIAMGroups(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error { tt, nn, e := scanIAMPolicies(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error { tt, nn, e := scanIAMInstanceProfiles(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error { tt, nn, e := scanIAMOIDCProviders(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error { tt, nn, e := scanIAMSAMLProviders(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error { tt, nn, e := scanIAMServerCertificates(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error { tt, nn, e := scanIAMVirtualMFADevices(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	if err = g1.Wait(); err != nil {
		return int(t.Load()), int(n.Load()), err
	}

	// Phase 2: per-principal resources — depend on phase 1 results being in DB.
	g2, ctx2 := errgroup.WithContext(ctx)
	g2.Go(func() error { tt, nn, e := scanIAMAccessKeys(ctx2, client, acct, st, scanID); add(tt, nn); return e })
	g2.Go(func() error { tt, nn, e := scanIAMRolePolicies(ctx2, client, acct, st, scanID); add(tt, nn); return e })
	g2.Go(func() error { tt, nn, e := scanIAMUserPolicies(ctx2, client, acct, st, scanID); add(tt, nn); return e })
	g2.Go(func() error { tt, nn, e := scanIAMGroupPolicies(ctx2, client, acct, st, scanID); add(tt, nn); return e })
	err = g2.Wait()
	return int(t.Load()), int(n.Load()), err
}

// scanIAMRoles lists all IAM roles, splitting service-linked roles (path prefix
// /aws-service-role/) into TypeIAMServiceLinkedRole.
func scanIAMRoles(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListRolesPaginator(client, &iam.ListRolesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListRoles", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListRoles: %w", err)
		}
		var batch []*store.Resource
		for _, role := range page.Roles {
			name := sv(role.RoleName)
			rt := TypeIAMRole
			if strings.HasPrefix(sv(role.Path), "/aws-service-role/") {
				rt = TypeIAMServiceLinkedRole
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           rt,
				NativeID:       sv(role.Arn),
				Name:           &name,
				CreatedAt:      tp(role.CreateDate),
				TagsJSON:       awsTagsJSON(role.Tags),
				AttributesJSON: mustJSON(role),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM roles/service-linked-roles: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

func scanIAMUsers(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListUsersPaginator(client, &iam.ListUsersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListUsers", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListUsers: %w", err)
		}
		var batch []*store.Resource
		for _, user := range page.Users {
			name := sv(user.UserName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMUser,
				NativeID:       sv(user.Arn),
				Name:           &name,
				CreatedAt:      tp(user.CreateDate),
				AttributesJSON: mustJSON(user),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM users: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

func scanIAMGroups(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListGroupsPaginator(client, &iam.ListGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListGroups", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListGroups: %w", err)
		}
		var batch []*store.Resource
		for _, group := range page.Groups {
			name := sv(group.GroupName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMGroup,
				NativeID:       sv(group.Arn),
				Name:           &name,
				CreatedAt:      tp(group.CreateDate),
				AttributesJSON: mustJSON(group),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanIAMPolicies lists all IAM managed policies — both AWS-managed and customer-managed.
func scanIAMPolicies(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	// PolicyScopeTypeLocal returns only customer-managed policies. PolicyScopeTypeAll
	// also returns ~1,500+ AWS-managed policies, which are public/static and not
	// useful for per-account discovery — and make IAM scanning ~15x slower.
	pager := iam.NewListPoliciesPaginator(client, &iam.ListPoliciesInput{Scope: iamtypes.PolicyScopeTypeLocal})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListPolicies", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListPolicies: %w", err)
		}
		var batch []*store.Resource
		for _, p := range page.Policies {
			name := sv(p.PolicyName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMPolicy,
				NativeID:       sv(p.Arn),
				Name:           &name,
				CreatedAt:      tp(p.CreateDate),
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM managed policies: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

func scanIAMInstanceProfiles(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListInstanceProfilesPaginator(client, &iam.ListInstanceProfilesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListInstanceProfiles", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListInstanceProfiles: %w", err)
		}
		var batch []*store.Resource
		for _, ip := range page.InstanceProfiles {
			name := sv(ip.InstanceProfileName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMInstanceProfile,
				NativeID:       sv(ip.Arn),
				Name:           &name,
				CreatedAt:      tp(ip.CreateDate),
				AttributesJSON: mustJSON(ip),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM instance profiles: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanIAMOIDCProviders fetches OIDC provider ARNs then describes each concurrently.
func scanIAMOIDCProviders(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("iam:ListOpenIDConnectProviders", acct.ID, "global", err)
		}
		return 0, 0, fmt.Errorf("iam:ListOpenIDConnectProviders: %w", err)
	}
	const maxConcurrent = 20
	sem := semaphore.NewWeighted(maxConcurrent)
	var mu sync.Mutex
	var batch []*store.Resource
	g, gctx := errgroup.WithContext(ctx)
	for _, ref := range out.OpenIDConnectProviderList {
		arn := sv(ref.Arn)
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			detail, err := client.GetOpenIDConnectProvider(gctx, &iam.GetOpenIDConnectProviderInput{OpenIDConnectProviderArn: &arn})
			if err != nil {
				if isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("iam:GetOpenIDConnectProvider %s: %w", arn, err)
			}
			// Derive a short name from the last segment of the ARN.
			name := arn[strings.LastIndex(arn, "/")+1:]
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMOIDCProvider,
				NativeID:       arn,
				Name:           &name,
				CreatedAt:      tp(detail.CreateDate),
				AttributesJSON: mustJSON(detail),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert IAM OIDC providers: %w", err)
	}
	return len(batch), n, nil
}

// scanIAMSAMLProviders fetches SAML provider ARNs then describes each concurrently.
func scanIAMSAMLProviders(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.ListSAMLProviders(ctx, &iam.ListSAMLProvidersInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("iam:ListSAMLProviders", acct.ID, "global", err)
		}
		return 0, 0, fmt.Errorf("iam:ListSAMLProviders: %w", err)
	}
	const maxConcurrent = 20
	sem := semaphore.NewWeighted(maxConcurrent)
	var mu sync.Mutex
	var batch []*store.Resource
	g, gctx := errgroup.WithContext(ctx)
	for _, ref := range out.SAMLProviderList {
		arn := sv(ref.Arn)
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			detail, err := client.GetSAMLProvider(gctx, &iam.GetSAMLProviderInput{SAMLProviderArn: &arn})
			if err != nil {
				if isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("iam:GetSAMLProvider %s: %w", arn, err)
			}
			name := arn[strings.LastIndex(arn, "/")+1:]
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMSAMLProvider,
				NativeID:       arn,
				Name:           &name,
				CreatedAt:      tp(detail.CreateDate),
				TagsJSON:       awsTagsJSON(detail.Tags),
				AttributesJSON: mustJSON(detail),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert IAM SAML providers: %w", err)
	}
	return len(batch), n, nil
}

func scanIAMServerCertificates(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListServerCertificatesPaginator(client, &iam.ListServerCertificatesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListServerCertificates", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListServerCertificates: %w", err)
		}
		var batch []*store.Resource
		for _, cert := range page.ServerCertificateMetadataList {
			name := sv(cert.ServerCertificateName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMServerCertificate,
				NativeID:       sv(cert.Arn),
				Name:           &name,
				CreatedAt:      tp(cert.UploadDate),
				AttributesJSON: mustJSON(cert),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM server certificates: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanIAMVirtualMFADevices lists virtual MFA devices. SerialNumber is the ARN.
func scanIAMVirtualMFADevices(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListVirtualMFADevicesPaginator(client, &iam.ListVirtualMFADevicesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("iam:ListVirtualMFADevices", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("iam:ListVirtualMFADevices: %w", err)
		}
		var batch []*store.Resource
		for _, dev := range page.VirtualMFADevices {
			serial := sv(dev.SerialNumber)
			name := serial[strings.LastIndex(serial, "/")+1:]
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMVirtualMFADevice,
				NativeID:       serial,
				Name:           &name,
				CreatedAt:      tp(dev.EnableDate),
				AttributesJSON: mustJSON(dev),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM virtual MFA devices: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanIAMAccessKeys iterates all users in the DB and lists their access keys.
// NativeID format: {userARN}/access-key/{keyID} — used by resolvers to link keys to users.
func scanIAMAccessKeys(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	users, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMUser},
		Limit:     util.AllResources,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list IAM users for access key scan: %w", err)
	}
	const maxConcurrent = 20
	sem := semaphore.NewWeighted(maxConcurrent)
	var mu sync.Mutex
	var batch []*store.Resource
	g, gctx := errgroup.WithContext(ctx)
	for _, u := range users {
		userARN := u.NativeID
		userName := sv(u.Name)
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			out, err := client.ListAccessKeys(gctx, &iam.ListAccessKeysInput{UserName: &userName})
			if err != nil {
				if isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("iam:ListAccessKeys %s: %w", userName, err)
			}
			var local []*store.Resource
			for _, key := range out.AccessKeyMetadata {
				keyID := sv(key.AccessKeyId)
				nativeID := userARN + "/access-key/" + keyID
				local = append(local, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeIAMAccessKey,
					NativeID:       nativeID,
					Name:           &keyID,
					CreatedAt:      tp(key.CreateDate),
					AttributesJSON: mustJSON(key),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			batch = append(batch, local...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert IAM access keys: %w", err)
	}
	return len(batch), n, nil
}

// scanIAMRolePolicies iterates all roles/service-linked roles and lists their inline policies.
// NativeID format: {roleARN}/policy/{policyName} — used by resolvers to link policies to roles.
func scanIAMRolePolicies(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	roles, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMRole, TypeIAMServiceLinkedRole},
		Limit:     util.AllResources,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list IAM roles for role policy scan: %w", err)
	}
	const maxConcurrent = 20
	sem := semaphore.NewWeighted(maxConcurrent)
	var mu sync.Mutex
	var batch []*store.Resource
	g, gctx := errgroup.WithContext(ctx)
	for _, role := range roles {
		roleARN := role.NativeID
		roleName := sv(role.Name)
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := iam.NewListRolePoliciesPaginator(client, &iam.ListRolePoliciesInput{RoleName: &roleName})
			var local []*store.Resource
			for pager.HasMorePages() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("iam:ListRolePolicies %s: %w", roleName, err)
				}
				for _, policyName := range page.PolicyNames {
					detail, err := client.GetRolePolicy(gctx, &iam.GetRolePolicyInput{
						RoleName: &roleName, PolicyName: &policyName,
					})
					if err != nil {
						if isAccessDenied(err) {
							continue
						}
						return fmt.Errorf("iam:GetRolePolicy %s/%s: %w", roleName, policyName, err)
					}
					nativeID := roleARN + "/policy/" + policyName
					local = append(local, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeIAMRolePolicy,
						NativeID:       nativeID,
						Name:           &policyName,
						AttributesJSON: mustJSON(detail),
						DiscoveredBy:   scanID,
					})
				}
			}
			mu.Lock()
			batch = append(batch, local...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert IAM role policies: %w", err)
	}
	return len(batch), n, nil
}

// scanIAMUserPolicies iterates all users and lists their inline policies.
// NativeID format: {userARN}/policy/{policyName}.
func scanIAMUserPolicies(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	users, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMUser},
		Limit:     util.AllResources,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list IAM users for user policy scan: %w", err)
	}
	const maxConcurrent = 20
	sem := semaphore.NewWeighted(maxConcurrent)
	var mu sync.Mutex
	var batch []*store.Resource
	g, gctx := errgroup.WithContext(ctx)
	for _, u := range users {
		userARN := u.NativeID
		userName := sv(u.Name)
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := iam.NewListUserPoliciesPaginator(client, &iam.ListUserPoliciesInput{UserName: &userName})
			var local []*store.Resource
			for pager.HasMorePages() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("iam:ListUserPolicies %s: %w", userName, err)
				}
				for _, policyName := range page.PolicyNames {
					detail, err := client.GetUserPolicy(gctx, &iam.GetUserPolicyInput{
						UserName: &userName, PolicyName: &policyName,
					})
					if err != nil {
						if isAccessDenied(err) {
							continue
						}
						return fmt.Errorf("iam:GetUserPolicy %s/%s: %w", userName, policyName, err)
					}
					nativeID := userARN + "/policy/" + policyName
					local = append(local, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeIAMUserPolicy,
						NativeID:       nativeID,
						Name:           &policyName,
						AttributesJSON: mustJSON(detail),
						DiscoveredBy:   scanID,
					})
				}
			}
			mu.Lock()
			batch = append(batch, local...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert IAM user policies: %w", err)
	}
	return len(batch), n, nil
}

// scanIAMGroupPolicies iterates all groups and lists their inline policies.
// NativeID format: {groupARN}/policy/{policyName}.
func scanIAMGroupPolicies(ctx context.Context, client *iam.Client, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMGroup},
		Limit:     util.AllResources,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list IAM groups for group policy scan: %w", err)
	}
	const maxConcurrent = 20
	sem := semaphore.NewWeighted(maxConcurrent)
	var mu sync.Mutex
	var batch []*store.Resource
	g, gctx := errgroup.WithContext(ctx)
	for _, grp := range groups {
		groupARN := grp.NativeID
		groupName := sv(grp.Name)
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := iam.NewListGroupPoliciesPaginator(client, &iam.ListGroupPoliciesInput{GroupName: &groupName})
			var local []*store.Resource
			for pager.HasMorePages() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("iam:ListGroupPolicies %s: %w", groupName, err)
				}
				for _, policyName := range page.PolicyNames {
					detail, err := client.GetGroupPolicy(gctx, &iam.GetGroupPolicyInput{
						GroupName: &groupName, PolicyName: &policyName,
					})
					if err != nil {
						if isAccessDenied(err) {
							continue
						}
						return fmt.Errorf("iam:GetGroupPolicy %s/%s: %w", groupName, policyName, err)
					}
					nativeID := groupARN + "/policy/" + policyName
					local = append(local, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeIAMGroupPolicy,
						NativeID:       nativeID,
						Name:           &policyName,
						AttributesJSON: mustJSON(detail),
						DiscoveredBy:   scanID,
					})
				}
			}
			mu.Lock()
			batch = append(batch, local...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert IAM group policies: %w", err)
	}
	return len(batch), n, nil
}
