package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
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
		emits: []coverage.TypeDecl{
			{Service: "iam", DiscoType: TypeIAMUser},
			{Service: "iam", DiscoType: TypeIAMGroup},
			{Service: "iam", DiscoType: TypeIAMRole},
			{Service: "iam", DiscoType: TypeIAMServiceLinkedRole},
			{Service: "iam", DiscoType: TypeIAMPolicy},
			{Service: "iam", DiscoType: TypeIAMRolePolicy},
			{Service: "iam", DiscoType: TypeIAMUserPolicy},
			{Service: "iam", DiscoType: TypeIAMGroupPolicy},
			{Service: "iam", DiscoType: TypeIAMAccessKey},
			{Service: "iam", DiscoType: TypeIAMInstanceProfile},
			{Service: "iam", DiscoType: TypeIAMOIDCProvider},
			{Service: "iam", DiscoType: TypeIAMSAMLProvider},
			{Service: "iam", DiscoType: TypeIAMServerCertificate},
			{Service: "iam", DiscoType: TypeIAMVirtualMFADevice},
		},
	})
}

// iamAPI is the narrow set of IAM operations called by the scanIAM sub-phases.
// `GetAccountAuthorizationDetails` consolidates what used to be 7 separate
// list/describe calls (roles, users, groups, policies + per-policy version,
// per-principal inline policies).
type iamAPI interface {
	GetAccountAuthorizationDetails(context.Context, *iam.GetAccountAuthorizationDetailsInput, ...func(*iam.Options)) (*iam.GetAccountAuthorizationDetailsOutput, error)
	ListPolicies(context.Context, *iam.ListPoliciesInput, ...func(*iam.Options)) (*iam.ListPoliciesOutput, error)
	ListInstanceProfiles(context.Context, *iam.ListInstanceProfilesInput, ...func(*iam.Options)) (*iam.ListInstanceProfilesOutput, error)
	ListOpenIDConnectProviders(context.Context, *iam.ListOpenIDConnectProvidersInput, ...func(*iam.Options)) (*iam.ListOpenIDConnectProvidersOutput, error)
	GetOpenIDConnectProvider(context.Context, *iam.GetOpenIDConnectProviderInput, ...func(*iam.Options)) (*iam.GetOpenIDConnectProviderOutput, error)
	ListSAMLProviders(context.Context, *iam.ListSAMLProvidersInput, ...func(*iam.Options)) (*iam.ListSAMLProvidersOutput, error)
	GetSAMLProvider(context.Context, *iam.GetSAMLProviderInput, ...func(*iam.Options)) (*iam.GetSAMLProviderOutput, error)
	ListServerCertificates(context.Context, *iam.ListServerCertificatesInput, ...func(*iam.Options)) (*iam.ListServerCertificatesOutput, error)
	ListVirtualMFADevices(context.Context, *iam.ListVirtualMFADevicesInput, ...func(*iam.Options)) (*iam.ListVirtualMFADevicesOutput, error)
	ListAccessKeys(context.Context, *iam.ListAccessKeysInput, ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error)
}

// scanIAM discovers all IAM resources. Phase 1 fans out the independent
// scanners in parallel:
//
//   - scanIAMAuthDetails: paginated GetAccountAuthorizationDetails returning
//     users + roles + groups + managed policies (Local + AWS scope) + their
//     attached managed policies + inline policies + permission boundaries in
//     one call. Replaces the historical 7-scanner phase split.
//   - scanIAMInstanceProfiles, scanIAMOIDCProviders, scanIAMSAMLProviders,
//     scanIAMServerCertificates, scanIAMVirtualMFADevices: independent APIs.
//
// Phase 2: scanIAMAccessKeys (per-user fan-out — needs users in DB).
//
// IAM is a global service scanned once per account.
func scanIAM(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iam.NewFromConfig(acct.cfg)
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }

	// Phase 1: independent scanners in parallel.
	g1, ctx1 := errgroup.WithContext(ctx)
	g1.Go(func() error { tt, nn, e := scanIAMAuthDetails(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error {
		tt, nn, e := scanIAMInstanceProfiles(ctx1, client, acct, st, scanID)
		add(tt, nn)
		return e
	})
	g1.Go(func() error { tt, nn, e := scanIAMOIDCProviders(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error { tt, nn, e := scanIAMSAMLProviders(ctx1, client, acct, st, scanID); add(tt, nn); return e })
	g1.Go(func() error {
		tt, nn, e := scanIAMServerCertificates(ctx1, client, acct, st, scanID)
		add(tt, nn)
		return e
	})
	g1.Go(func() error {
		tt, nn, e := scanIAMVirtualMFADevices(ctx1, client, acct, st, scanID)
		add(tt, nn)
		return e
	})
	if err = g1.Wait(); err != nil {
		return int(t.Load()), int(n.Load()), err
	}

	// Phase 2: scanIAMAccessKeys depends on user list being in the DB.
	tt, nn, e := scanIAMAccessKeys(ctx, client, acct, st, scanID)
	add(tt, nn)
	return int(t.Load()), int(n.Load()), e
}

// scanIAMAuthDetails paginates iam:GetAccountAuthorizationDetails to upsert
// users + roles + groups + managed policies (Local + AWS scope) + their
// inline policies in a single API call sequence. Replaces the previous
// 7-scanner phase split (ListRoles, ListUsers, ListGroups, ListPolicies +
// per-policy GetPolicyVersion fan-out, plus ListRolePolicies / ListUserPolicies
// / ListGroupPolicies + per-name Get*Policy fan-outs). MaxItems=1000 (API
// ceiling) cuts page count.
//
// Filter requests all five entity types in one call. AccessDenied degrades
// to a single warning and an empty scan — no per-entity-type fallback.
//
// **AWS-managed catalogue gotcha**: GAAD's `AWSManagedPolicy` filter returns
// only AWS-managed policies *currently attached* to a principal, NOT the
// full ~1500-policy catalogue. To preserve the legacy "scan every AWS-
// managed policy" behaviour, scanIAMAuthDetails first runs a stub
// `ListPolicies(Scope=AWS)` pass (metadata only, no document enrichment —
// avoids the GetPolicyVersion fan-out that triggered IAM throttling).
// Order: GAAD first (rich attached-policy + entity rows, captures attached
// AWS-managed ARNs), then catalogue stub for the unattached remainder.
// Reversing the historical catalogue-first order keeps each row upserted
// exactly once so per-service totals == inserted on a fresh DB. Unattached
// catalogue policies keep their stub form — they exist as FK targets for
// resolveManagedPolicyAttachments and the walker silently skips no-document
// rows.
func scanIAMAuthDetails(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	attachedManagedARNs := map[string]bool{}

	maxItems := int32(1000)
	pager := iam.NewGetAccountAuthorizationDetailsPaginator(client, &iam.GetAccountAuthorizationDetailsInput{
		Filter: []iamtypes.EntityType{
			iamtypes.EntityTypeUser,
			iamtypes.EntityTypeRole,
			iamtypes.EntityTypeGroup,
			iamtypes.EntityTypeLocalManagedPolicy,
			iamtypes.EntityTypeAWSManagedPolicy,
		},
		MaxItems: &maxItems,
	})
	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "iam:GetAccountAuthorizationDetails", acct.ID, "global", perr)
			}
			return total, inserted, fmt.Errorf("iam:GetAccountAuthorizationDetails: %w", perr)
		}

		var batch []*store.Resource

		// Roles + their inline policies. Service-linked roles split off via
		// the `/aws-service-role/` path prefix, same as the legacy scanner.
		for _, r := range page.RoleDetailList {
			name := sv(r.RoleName)
			rt := TypeIAMRole
			isSLR := strings.HasPrefix(sv(r.Path), "/aws-service-role/")
			if isSLR {
				rt = TypeIAMServiceLinkedRole
			}
			batch = append(batch, &store.Resource{
				Provider:          "aws",
				AccountID:         acct.ID,
				AccountName:       &acct.Name,
				Type:              rt,
				NativeID:          sv(r.Arn),
				Name:              &name,
				CreatedAt:         tp(r.CreateDate),
				TagsJSON:          awsTagsJSON(r.Tags),
				AttributesJSON:    mustJSON(r),
				DiscoveredBy:      scanID,
				ManagedByProvider: isSLR,
			})
			batch = append(batch, inlinePolicyRows(r.RolePolicyList, sv(r.Arn), TypeIAMRolePolicy, acct, scanID)...)
		}

		// Users + their inline policies. PermissionsBoundary, GroupList, and
		// Tags ride along on UserDetail; downstream resolvers consume them
		// from the same JSON shape as before.
		for _, u := range page.UserDetailList {
			name := sv(u.UserName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMUser,
				NativeID:       sv(u.Arn),
				Name:           &name,
				CreatedAt:      tp(u.CreateDate),
				TagsJSON:       awsTagsJSON(u.Tags),
				AttributesJSON: mustJSON(u),
				DiscoveredBy:   scanID,
			})
			batch = append(batch, inlinePolicyRows(u.UserPolicyList, sv(u.Arn), TypeIAMUserPolicy, acct, scanID)...)
		}

		// Groups + their inline policies.
		for _, gr := range page.GroupDetailList {
			name := sv(gr.GroupName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIAMGroup,
				NativeID:       sv(gr.Arn),
				Name:           &name,
				CreatedAt:      tp(gr.CreateDate),
				AttributesJSON: mustJSON(gr),
				DiscoveredBy:   scanID,
			})
			batch = append(batch, inlinePolicyRows(gr.GroupPolicyList, sv(gr.Arn), TypeIAMGroupPolicy, acct, scanID)...)
		}

		// Managed policies. PolicyVersionList carries every version; pick the
		// IsDefaultVersion=true entry to populate the wrapped `PolicyVersion`
		// field that resolveIAMPolicyResources reads. AWS-managed catalogue
		// detected via the `arn:aws:iam::aws:` prefix (canonical AWS-curated
		// scope marker); GAAD does not expose scope per-policy.
		for _, p := range page.Policies {
			arn := sv(p.Arn)
			name := sv(p.PolicyName)
			managed := strings.HasPrefix(arn, "arn:aws:iam::aws:")
			if managed {
				attachedManagedARNs[arn] = true
			}
			wrapped := struct {
				Policy        iamtypes.ManagedPolicyDetail `json:"Policy"`
				PolicyVersion *iamtypes.PolicyVersion      `json:"PolicyVersion,omitempty"`
			}{Policy: p, PolicyVersion: defaultPolicyVersion(p)}
			batch = append(batch, &store.Resource{
				Provider:          "aws",
				AccountID:         acct.ID,
				AccountName:       &acct.Name,
				Type:              TypeIAMPolicy,
				NativeID:          arn,
				Name:              &name,
				CreatedAt:         tp(p.CreateDate),
				AttributesJSON:    mustJSON(wrapped),
				DiscoveredBy:      scanID,
				ManagedByProvider: managed,
			})
		}

		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM auth details: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}

	t0, n0, err := scanIAMAWSManagedCatalogue(ctx, client, acct, st, scanID, attachedManagedARNs)
	total += t0
	inserted += n0
	return total, inserted, err
}

// scanIAMAWSManagedCatalogue lists every AWS-managed policy ARN
// (`Scope=AWS`) and upserts a stub row per policy that the prior GAAD pass
// did not already enrich. No GetPolicyVersion enrichment — those rows carry
// metadata only. ListPolicies returns ~1500 entries across ~15 paginated
// calls — cheap, no per-policy fan-out, no throttling risk. `skipARNs`
// holds the attached-managed ARN set GAAD already inserted with rich
// PolicyVersion bodies; the catalogue pass skips those to avoid a redundant
// second upsert that would inflate the per-service `total` count above
// `inserted`.
func scanIAMAWSManagedCatalogue(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string, skipARNs map[string]bool) (total, inserted int, err error) {
	pager := iam.NewListPoliciesPaginator(client, &iam.ListPoliciesInput{
		Scope: iamtypes.PolicyScopeTypeAws,
	})
	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "iam:ListPolicies", acct.ID, "global", perr)
			}
			return total, inserted, fmt.Errorf("iam:ListPolicies(AWS): %w", perr)
		}
		var batch []*store.Resource
		for _, p := range page.Policies {
			arn := sv(p.Arn)
			if skipARNs[arn] {
				continue
			}
			name := sv(p.PolicyName)
			// Wrap shape mirrors the GAAD-pass output so resolvers see one
			// consistent JSON shape regardless of whether the policy was
			// attached. PolicyVersion is omitted (no enrichment).
			wrapped := struct {
				Policy        iamtypes.Policy         `json:"Policy"`
				PolicyVersion *iamtypes.PolicyVersion `json:"PolicyVersion,omitempty"`
			}{Policy: p}
			batch = append(batch, &store.Resource{
				Provider:          "aws",
				AccountID:         acct.ID,
				AccountName:       &acct.Name,
				Type:              TypeIAMPolicy,
				NativeID:          arn,
				Name:              &name,
				CreatedAt:         tp(p.CreateDate),
				AttributesJSON:    mustJSON(wrapped),
				DiscoveredBy:      scanID,
				ManagedByProvider: true,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert AWS-managed policy catalogue: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// inlinePolicyRows projects a slice of GAAD-embedded inline policies into
// store.Resource rows. NativeID format `{parentARN}/policy/{policyName}` is
// preserved from the legacy scanIAM{Role,User,Group}Policies output so the
// inline-policy resolvers (parent-link extraction in resolveInlinePolicyParents)
// keep working unchanged.
func inlinePolicyRows(list []iamtypes.PolicyDetail, parentARN, rtype string, acct *account, scanID string) []*store.Resource {
	if len(list) == 0 {
		return nil
	}
	out := make([]*store.Resource, 0, len(list))
	for _, ip := range list {
		policyName := sv(ip.PolicyName)
		if policyName == "" {
			continue
		}
		out = append(out, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           rtype,
			NativeID:       parentARN + "/policy/" + policyName,
			Name:           &policyName,
			AttributesJSON: mustJSON(ip),
			DiscoveredBy:   scanID,
		})
	}
	return out
}

// defaultPolicyVersion picks the IsDefaultVersion=true entry from a managed
// policy's PolicyVersionList. The walker reads `PolicyVersion.Document`; we
// keep the wrap shape compatible with the legacy ListPolicies +
// GetPolicyVersion path so resolveIAMPolicyResources needs no changes.
func defaultPolicyVersion(p iamtypes.ManagedPolicyDetail) *iamtypes.PolicyVersion {
	for i := range p.PolicyVersionList {
		v := &p.PolicyVersionList[i]
		if v.IsDefaultVersion {
			return v
		}
	}
	return nil
}

func scanIAMInstanceProfiles(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListInstanceProfilesPaginator(client, &iam.ListInstanceProfilesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "iam:ListInstanceProfiles", acct.ID, "global", err)
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
func scanIAMOIDCProviders(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "iam:ListOpenIDConnectProviders", acct.ID, "global", err)
		}
		return 0, 0, fmt.Errorf("iam:ListOpenIDConnectProviders: %w", err)
	}
	sem := semaphore.NewWeighted(fanoutHigh)
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
func scanIAMSAMLProviders(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.ListSAMLProviders(ctx, &iam.ListSAMLProvidersInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "iam:ListSAMLProviders", acct.ID, "global", err)
		}
		return 0, 0, fmt.Errorf("iam:ListSAMLProviders: %w", err)
	}
	sem := semaphore.NewWeighted(fanoutHigh)
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

func scanIAMServerCertificates(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListServerCertificatesPaginator(client, &iam.ListServerCertificatesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "iam:ListServerCertificates", acct.ID, "global", err)
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
func scanIAMVirtualMFADevices(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := iam.NewListVirtualMFADevicesPaginator(client, &iam.ListVirtualMFADevicesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "iam:ListVirtualMFADevices", acct.ID, "global", err)
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
func scanIAMAccessKeys(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	users, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeIAMUser},
		Limit:     util.AllResources,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list IAM users for access key scan: %w", err)
	}
	sem := semaphore.NewWeighted(fanoutHigh)
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
