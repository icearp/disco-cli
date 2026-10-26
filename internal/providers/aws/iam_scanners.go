package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIAMAccount, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMUser, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMGroup, Service: "iam", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeIAMRole, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMServiceLinkedRole, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMPolicy, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMRolePolicy, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMUserPolicy, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMGroupPolicy, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMAccessKey, Service: "iam", Redact: []redact.Rule{{Path: "SecretAccessKey", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeIAMInstanceProfile, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMOIDCProvider, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMSAMLProvider, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMServerCertificate, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMVirtualMFADevice, Service: "iam"})
	// ListServiceSpecificCredentials returns metadata only: the password and API
	// key appear solely in the Create/Reset response, so there is nothing to redact.
	registerType(restype.Descriptor{Type: TypeIAMServiceSpecificCredential, Service: "iam"})
	// CertificateBody is the public X.509 certificate; its private key never
	// reaches AWS, so the body is kept as the certificate's identity.
	registerType(restype.Descriptor{Type: TypeIAMSigningCertificate, Service: "iam"})
	registerType(restype.Descriptor{Type: TypeIAMSSHPublicKey, Service: "iam"})
	// Nothing to redact: SendDelegationToken hands the partner its token out of
	// band, and the listed request carries no credential.
	registerType(restype.Descriptor{Type: TypeIAMDelegationRequest, Service: "iam", Untaggable: true})
	registerService(serviceEntry{
		name:   "aws:iam",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			return scanIAM(ctx, acct, st, scanID)
		},
	})
}

// iamAPI is the narrow set of IAM operations the scanIAM sub-phases call.
// `GetAccountAuthorizationDetails` consolidates 7 former separate list/describe
// calls (roles, users, groups, policies + per-policy version, per-principal
// inline policies).
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
	GetAccountSummary(context.Context, *iam.GetAccountSummaryInput, ...func(*iam.Options)) (*iam.GetAccountSummaryOutput, error)
	ListAccountAliases(context.Context, *iam.ListAccountAliasesInput, ...func(*iam.Options)) (*iam.ListAccountAliasesOutput, error)
	GetAccountPasswordPolicy(context.Context, *iam.GetAccountPasswordPolicyInput, ...func(*iam.Options)) (*iam.GetAccountPasswordPolicyOutput, error)
	ListServiceSpecificCredentials(context.Context, *iam.ListServiceSpecificCredentialsInput, ...func(*iam.Options)) (*iam.ListServiceSpecificCredentialsOutput, error)
	ListSigningCertificates(context.Context, *iam.ListSigningCertificatesInput, ...func(*iam.Options)) (*iam.ListSigningCertificatesOutput, error)
	ListSSHPublicKeys(context.Context, *iam.ListSSHPublicKeysInput, ...func(*iam.Options)) (*iam.ListSSHPublicKeysOutput, error)
	ListDelegationRequests(context.Context, *iam.ListDelegationRequestsInput, ...func(*iam.Options)) (*iam.ListDelegationRequestsOutput, error)
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
// Phase 3: the other per-user credentials (service-specific credentials,
// signing certificates, SSH public keys).
//
// Phase 4: scanIAMDelegationRequests, which needs no user list.
//
// Every phase runs whatever the others return, so a failure in one costs no
// other phase its rows; phases 2-3 cover the users phase 1 stored before any
// failure. Phase 1 does not cancel its siblings on the first error for the
// same reason. The errors are joined by phaseErrs, so a lone or all-transient
// failure still marks the scan partial.
//
// IAM is a global service scanned once per account.
func scanIAM(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iam.NewFromConfig(acct.cfg)
	var (
		t, n atomic.Int64
		mu   sync.Mutex
		errs phaseErrs
	)
	addErr := func(op string, e error) {
		mu.Lock()
		defer mu.Unlock()
		errs.add(op, e)
	}
	collect := func(op string, tt, nn int, e error) {
		t.Add(int64(tt))
		n.Add(int64(nn))
		addErr(op, e)
	}

	// Phase 1: independent scanners in parallel.
	var wg sync.WaitGroup
	wg.Go(func() {
		tt, nn := scanIAMAccount(ctx, client, acct, st, scanID, addErr)
		t.Add(int64(tt))
		n.Add(int64(nn))
	})
	wg.Go(func() {
		tt, nn, e := scanIAMAuthDetails(ctx, client, acct, st, scanID)
		collect("iam:GetAccountAuthorizationDetails", tt, nn, e)
	})
	wg.Go(func() {
		tt, nn, e := scanIAMInstanceProfiles(ctx, client, acct, st, scanID)
		collect("iam:ListInstanceProfiles", tt, nn, e)
	})
	wg.Go(func() {
		tt, nn, e := scanIAMOIDCProviders(ctx, client, acct, st, scanID)
		collect(iamListOIDCProvidersOp, tt, nn, e)
	})
	wg.Go(func() {
		tt, nn, e := scanIAMSAMLProviders(ctx, client, acct, st, scanID)
		collect(iamListSAMLProvidersOp, tt, nn, e)
	})
	wg.Go(func() {
		tt, nn, e := scanIAMServerCertificates(ctx, client, acct, st, scanID)
		collect("iam:ListServerCertificates", tt, nn, e)
	})
	wg.Go(func() {
		tt, nn, e := scanIAMVirtualMFADevices(ctx, client, acct, st, scanID)
		collect("iam:ListVirtualMFADevices", tt, nn, e)
	})
	wg.Wait()

	// Phases 2-3 list per-user credentials for the users phase 1 stored.
	if users, uerr := iamScannedUsers(st, acct, scanID); uerr != nil {
		collect("iam user read-back", 0, 0, uerr)
	} else {
		tt, nn, e := scanIAMAccessKeys(ctx, client, users, acct, st, scanID)
		collect("iam:ListAccessKeys", tt, nn, e)
		tt, nn, e = scanIAMServiceSpecificCredentials(ctx, client, users, acct, st, scanID)
		collect("iam:ListServiceSpecificCredentials", tt, nn, e)
		tt, nn, e = scanIAMSigningCertificates(ctx, client, users, acct, st, scanID)
		collect("iam:ListSigningCertificates", tt, nn, e)
		tt, nn, e = scanIAMSSHPublicKeys(ctx, client, users, acct, st, scanID)
		collect("iam:ListSSHPublicKeys", tt, nn, e)
	}

	tt, nn, e := scanIAMDelegationRequests(ctx, client, acct, st, scanID)
	collect("iam:ListDelegationRequests", tt, nn, e)
	return int(t.Load()), int(n.Load()), errs.join(st, acct, "global")
}

// iamAccountNativeID builds the aws:iam:account self-node key, shared by
// scanIAMAccount and every resolver that references an account, so a
// placeholder and the scanned row share one identity.
func iamAccountNativeID(partition, accountID string) string {
	return arnFor(partition, "iam", "", accountID, "root")
}

// iamAccountPlaceholder builds the aws:iam:account row a resolver inserts with
// InsertResourcesIfAbsent for an account it references but did not scan. The
// first resolver to insert wins, so every resolver must build the same row.
func iamAccountPlaceholder(partition, accountID, scanID string) *store.Resource {
	name := accountID
	return &store.Resource{
		Provider:       "aws",
		AccountID:      accountID,
		Type:           TypeIAMAccount,
		NativeID:       iamAccountNativeID(partition, accountID),
		Name:           &name,
		Region:         regionGlobal,
		AttributesJSON: "{}",
		DiscoveredBy:   scanID,
	}
}

// scanIAMAccount upserts the account self-node (aws:iam:account) carrying
// account-level posture: IAM account summary, account alias (if any), and
// password policy (absent if unconfigured). One row per scanned account, keyed
// NativeID = arn:{p}:iam::<acct>:root — the same natural key the resolvers
// that insert account placeholder rows use, so a foreign account first seen
// by reference version-populates its empty placeholder once directly scanned.
//
// A denied read warns and the row is stored without its key: the denial
// lasts until the grant changes. Any other failed read is passed to add under
// its op and the row is not stored this scan: stored without the key, the row
// would version-split and record the summary, alias or password policy as
// removed, a change that did not happen. An upsert failure is passed to add
// too.
func scanIAMAccount(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string, add func(op string, err error)) (int, int) {
	attrs := map[string]any{}
	withhold := false
	failed := func(op string, err error) {
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, op, acct.ID, "global", err)
			return
		}
		withhold = true
		add(op, fmt.Errorf("%s: %w", op, err))
	}
	if summary, err := client.GetAccountSummary(ctx, &iam.GetAccountSummaryInput{}); err != nil {
		failed("iam:GetAccountSummary", err)
	} else {
		attrs["SummaryMap"] = summary.SummaryMap
	}
	name := acct.ID
	if aliases, err := client.ListAccountAliases(ctx, &iam.ListAccountAliasesInput{}); err != nil {
		failed("iam:ListAccountAliases", err)
	} else {
		attrs["AccountAliases"] = aliases.AccountAliases
		if len(aliases.AccountAliases) > 0 {
			name = aliases.AccountAliases[0]
		}
	}
	// Password policy is absent on accounts that never configured one;
	// NoSuchEntity (in the op's model) is that default state, not an error.
	switch pw, err := client.GetAccountPasswordPolicy(ctx, &iam.GetAccountPasswordPolicyInput{}); {
	case err == nil:
		attrs["PasswordPolicy"] = pw.PasswordPolicy
	case !isAPIErrorCode(err, "NoSuchEntity"):
		failed("iam:GetAccountPasswordPolicy", err)
	}
	if withhold {
		return 0, 0
	}

	row := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		Type:           TypeIAMAccount,
		NativeID:       iamAccountNativeID(acct.partition(), acct.ID),
		Name:           &name,
		Region:         regionGlobal,
		AttributesJSON: mustJSON(attrs),
		DiscoveredBy:   scanID,
	}
	total, inserted, err := upsertBatch(st, []*store.Resource{row}, "iam account")
	add("iam account upsert", err)
	return total, inserted
}

// scanIAMAuthDetails paginates iam:GetAccountAuthorizationDetails to upsert
// users + roles + groups + managed policies (Local + AWS scope) + their inline
// policies in one API call sequence. Replaces the former 7-scanner phase split
// (ListRoles, ListUsers, ListGroups, ListPolicies + per-policy GetPolicyVersion
// fan-out, plus ListRolePolicies/ListUserPolicies/ListGroupPolicies + per-name
// Get*Policy fan-outs). MaxItems=1000 (API ceiling) cuts page count.
//
// Filter requests all five entity types in one call. AccessDenied degrades to
// a single warning and an empty scan — no per-entity-type fallback.
//
// **AWS-managed catalogue gotcha**: GAAD's `AWSManagedPolicy` filter returns
// only AWS-managed policies *currently attached* to a principal, not the full
// ~1500-policy catalogue. To preserve the legacy "scan every AWS-managed
// policy" behavior, scanIAMAuthDetails first runs a stub `ListPolicies(Scope=AWS)`
// pass (metadata only, no document enrichment — avoids the GetPolicyVersion
// fan-out that triggered IAM throttling). Order: GAAD first (captures attached
// AWS-managed ARNs), then the catalogue stub for the unattached remainder —
// reversing the historical catalogue-first order keeps each row upserted
// exactly once so per-service totals == inserted on a fresh DB. Unattached
// catalogue policies keep their stub form as FK targets for
// resolveManagedPolicyAttachments; the walker silently skips no-document rows.
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
				Region:            regionGlobal,
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
				Region:         regionGlobal,
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
				Region:         regionGlobal,
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
				Region:            regionGlobal,
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

// scanIAMAWSManagedCatalogue lists every AWS-managed policy ARN (`Scope=AWS`)
// and upserts a stub row per policy the prior GAAD pass didn't already enrich.
// No GetPolicyVersion enrichment — these rows carry metadata only. ListPolicies
// returns ~1500 entries across ~15 paginated calls — cheap, no per-policy
// fan-out, no throttling risk. `skipARNs` holds the attached-managed ARN set
// GAAD already inserted with rich PolicyVersion bodies; the catalogue pass
// skips those to avoid a redundant second upsert that would inflate `total`
// above `inserted`.
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
			// consistent JSON shape regardless of attachment. PolicyVersion
			// omitted (no enrichment).
			wrapped := struct {
				Policy        iamtypes.Policy         `json:"Policy"`
				PolicyVersion *iamtypes.PolicyVersion `json:"PolicyVersion,omitempty"`
			}{Policy: p}
			batch = append(batch, &store.Resource{
				Provider:          "aws",
				AccountID:         acct.ID,
				AccountName:       &acct.Name,
				Region:            regionGlobal,
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

// inlinePolicyRows projects GAAD-embedded inline policies into store.Resource
// rows. NativeID format `{parentARN}/policy/{policyName}` matches the legacy
// scanIAM{Role,User,Group}Policies output so resolveInlinePolicyParents
// (parent-link extraction) keeps working unchanged.
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
			Region:         regionGlobal,
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
				Region:         regionGlobal,
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

const (
	iamListOIDCProvidersOp = "iam:ListOpenIDConnectProviders"
	iamGetOIDCProviderOp   = "iam:GetOpenIDConnectProvider"
	iamListSAMLProvidersOp = "iam:ListSAMLProviders"
	iamGetSAMLProviderOp   = "iam:GetSAMLProvider"
)

// scanIAMOIDCProviders stores each listed OIDC provider from its
// GetOpenIDConnectProvider body, the only source of its tags, as
// storeIAMProviders says.
func scanIAMOIDCProviders(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	out, listErr := client.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	var arns []*string
	if listErr == nil {
		for _, ref := range out.OpenIDConnectProviderList {
			arns = append(arns, ref.Arn)
		}
	}
	return storeIAMProviders(ctx, st, acct, iamListOIDCProvidersOp, iamGetOIDCProviderOp, "OIDC", arns, listErr,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			detail, err := client.GetOpenIDConnectProvider(ctx, &iam.GetOpenIDConnectProviderInput{OpenIDConnectProviderArn: &arn})
			if err != nil {
				return nil, err
			}
			return iamProviderRow(acct, scanID, TypeIAMOIDCProvider, arn, detail.CreateDate, awsTagsJSON(detail.Tags), detail), nil
		})
}

// scanIAMSAMLProviders stores each listed SAML provider from its
// GetSAMLProvider body, as storeIAMProviders says. The body's PrivateKeyList
// holds key ids and upload times only, no key material.
func scanIAMSAMLProviders(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	out, listErr := client.ListSAMLProviders(ctx, &iam.ListSAMLProvidersInput{})
	var arns []*string
	if listErr == nil {
		for _, ref := range out.SAMLProviderList {
			arns = append(arns, ref.Arn)
		}
	}
	return storeIAMProviders(ctx, st, acct, iamListSAMLProvidersOp, iamGetSAMLProviderOp, "SAML", arns, listErr,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			detail, err := client.GetSAMLProvider(ctx, &iam.GetSAMLProviderInput{SAMLProviderArn: &arn})
			if err != nil {
				return nil, err
			}
			return iamProviderRow(acct, scanID, TypeIAMSAMLProvider, arn, detail.CreateDate, awsTagsJSON(detail.Tags), detail), nil
		})
}

// storeIAMProviders reads each identity provider an ARN-only listing (listOp,
// which ended with listErr) returned, through get (getOp), and stores the rows
// get builds from the read bodies.
//
// One provider's failure costs no other provider its row. The list entry holds
// nothing but the ARN, so a provider whose read fails has no row to fall back
// on and is not stored: a provider deleted since it was listed (NoSuchEntity,
// in both Get ops' models) silently, a denied one with one warning for all of
// them, and any other failure, transient included, is returned after the rest
// are stored, so the scan is marked partial. A list entry with no ARN warns.
// kind names the providers ("OIDC") in the warnings.
func storeIAMProviders(
	ctx context.Context, st *store.Store, acct *account, listOp, getOp, kind string,
	listed []*string, listErr error,
	get func(ctx context.Context, arn string) (*store.Resource, error),
) (int, int, error) {
	var (
		arns          []string
		noARN, denied skipTally
		rows          []*store.Resource
		errs          []error
	)
	for i, arn := range listed {
		if sv(arn) == "" {
			noARN.skipped(fmt.Sprintf("list entry %d", i), nil)
			continue
		}
		arns = append(arns, *arn)
	}
	reads, stopped := readEachKey(ctx, arns, func(ctx context.Context, arn string) iamProviderRead {
		row, err := get(ctx, arn)
		switch {
		case err == nil:
			return iamProviderRead{row: row}
		case isAPIErrorCode(err, "NoSuchEntity"):
			return iamProviderRead{}
		case isAccessDenied(err):
			return iamProviderRead{denied: err}
		default:
			return iamProviderRead{err: fmt.Errorf("%s %s: %w", getOp, arn, err)}
		}
	})
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %s providers: %w", getOp, kind, stopped))
	}
	for i, r := range reads {
		switch {
		case r.row != nil:
			rows = append(rows, r.row)
		case r.denied != nil:
			denied.skipped(arns[i], r.denied)
		case r.err != nil:
			errs = append(errs, r.err)
		}
	}
	return storeReadRows(st, acct, "global", listOp, rows, listErr, func() {
		warnNotStored(st, listOp, acct, "global", &noARN, kind+" providers have an empty Arn")
		denied.warnDeniedIn(st, getOp, acct, "global", kind+" provider(s) not stored: their Get was")
	}, errs...)
}

// iamProviderRead is one listed provider's read: its row, or the denial or
// other error that left it without one. All three are empty for a provider
// deleted since it was listed.
type iamProviderRead struct {
	row         *store.Resource
	denied, err error
}

// iamProviderRow builds an identity provider's row from its Get body, named
// after the ARN's last path segment.
func iamProviderRow(acct *account, scanID, typ, arn string, created *time.Time, tags *string, body any) *store.Resource {
	name := arn[strings.LastIndex(arn, "/")+1:]
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Region:         regionGlobal,
		Type:           typ,
		NativeID:       arn,
		Name:           &name,
		CreatedAt:      tp(created),
		TagsJSON:       tags,
		AttributesJSON: mustJSON(body),
		DiscoveredBy:   scanID,
	}
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
				Region:         regionGlobal,
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
				Region:         regionGlobal,
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

// scanIAMAccessKeys lists the access keys of the users this scan stored.
// NativeID format: {userARN}/access-key/{keyID} — used by resolvers to link keys to users.
//
// No user's failure cancels another's: every key read is stored before the
// errors are returned. A user deleted since GAAD (NoSuchEntity) is skipped; a
// transient or denied call skips that user with one warning per cause.
func scanIAMAccessKeys(ctx context.Context, client iamAPI, users []childParent, acct *account, st *store.Store, scanID string) (int, int, error) {
	const op = "iam:ListAccessKeys"
	var (
		mu        sync.Mutex
		batch     []*store.Resource
		errs      []error
		transient transientTally
		denied    skipTally
		noID      skipTally
		wg        sync.WaitGroup
	)
	sem := make(chan struct{}, fanoutHigh)
	for _, u := range users {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			rows, err := listIAMAccessKeys(ctx, client, acct, scanID, u, &noID)
			mu.Lock()
			defer mu.Unlock()
			batch = append(batch, rows...)
			switch {
			case err == nil, isIAMUserGone(err), transient.drop(u.id, err):
			case isAccessDenied(err):
				denied.skipped(u.id, err)
			default:
				errs = append(errs, fmt.Errorf("%s %s: %w", op, u.id, err))
			}
		})
	}
	wg.Wait()
	transient.warnSkipped(st, op, acct, "global")
	denied.warnDenied(st, op, acct.ID, "user(s)")
	warnNotStored(st, op, acct, "global", &noID, "access keys without AccessKeyId, the NativeID's key, by user name")
	total, inserted, err := upsertBatch(st, batch, "IAM access keys")
	return total, inserted, errors.Join(append(errs, err)...)
}

// listIAMAccessKeys reads every page of one user's access keys, returning the
// rows read before any error with it. A key without an AccessKeyId (optional
// in the model) has no NativeID and is tallied in noID instead.
func listIAMAccessKeys(ctx context.Context, client iamAPI, acct *account, scanID string, u childParent, noID *skipTally) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := iam.NewListAccessKeysPaginator(client, &iam.ListAccessKeysInput{UserName: &u.id})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return rows, err
		}
		for _, key := range out.AccessKeyMetadata {
			keyID := sv(key.AccessKeyId)
			if keyID == "" {
				noID.skipped(u.id, nil)
				continue
			}
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeIAMAccessKey,
				NativeID:       u.arn + "/access-key/" + keyID,
				Name:           &keyID,
				CreatedAt:      tp(key.CreateDate),
				AttributesJSON: mustJSON(key),
				DiscoveredBy:   scanID,
			})
		}
	}
	return rows, nil
}

// iamScannedUsers returns the users this scan stored, keyed by user name for
// the per-user list ops.
func iamScannedUsers(st *store.Store, acct *account, scanID string) ([]childParent, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIAMUser},
		SeenBy: scanID, Limit: util.AllResources,
	})
	if err != nil {
		return nil, storeReadError("list IAM users for per-user credential scans", err)
	}
	users := make([]childParent, len(rows))
	for i, r := range rows {
		users[i] = childParent{id: sv(r.Name), arn: r.NativeID}
	}
	return users, nil
}

// isIAMUserGone matches a user deleted between the GAAD pass and its child call.
func isIAMUserGone(err error) bool {
	return isAPIErrorCode(err, "NoSuchEntity")
}

// iamUserChildRow builds a per-user credential row. These credentials carry no
// ARN, so NativeID is {userARN}/{kind}/{id}, the access-key shape.
func iamUserChildRow(acct *account, scanID, typ, userARN, kind, id string, status iamtypes.StatusType, created *time.Time, elem any) *store.Resource {
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Region:         regionGlobal,
		Type:           typ,
		NativeID:       userARN + "/" + kind + "/" + id,
		Name:           &id,
		Status:         nonEmptyPtr(string(status)),
		CreatedAt:      tp(created),
		AttributesJSON: mustJSON(elem),
		DiscoveredBy:   scanID,
	}
}

// scanIAMServiceSpecificCredentials lists each user's service-specific
// credentials (CodeCommit, Keyspaces, Bedrock and CloudWatch Logs API keys).
func scanIAMServiceSpecificCredentials(ctx context.Context, client iamAPI, users []childParent, acct *account, st *store.Store, scanID string) (int, int, error) {
	var idless skipTally
	total, inserted, err := childFanOut(ctx, st, acct, "global", "iam:ListServiceSpecificCredentials", users, isIAMUserGone,
		func(gctx context.Context, u childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			// Per user, not AllUsers: no live call has confirmed what AllUsers
			// returns, and if ignored it would silently list only the caller's own.
			input := &iam.ListServiceSpecificCredentialsInput{UserName: &u.id}
			for {
				out, err := client.ListServiceSpecificCredentials(gctx, input)
				if err != nil {
					return nil, err
				}
				for _, c := range out.ServiceSpecificCredentials {
					id := sv(c.ServiceSpecificCredentialId)
					if id == "" {
						idless.skipped(u.arn, nil)
						continue
					}
					rows = append(rows, iamUserChildRow(acct, scanID, TypeIAMServiceSpecificCredential, u.arn, "service-specific-credential", id, c.Status, c.CreateDate, c))
				}
				// This op has no SDK paginator. A repeated marker keeps the rows
				// read and returns errRepeatedNextToken, so the listing is not
				// stored as complete.
				if last, err := lastPage(input.Marker, out.Marker); last {
					return rows, err
				}
				input.Marker = out.Marker
			}
		})
	warnNotStored(st, "iam:ListServiceSpecificCredentials", acct, "global", &idless, "service-specific credentials have no ServiceSpecificCredentialId")
	return total, inserted, err
}

// scanIAMSigningCertificates lists each user's X.509 signing certificates.
func scanIAMSigningCertificates(ctx context.Context, client iamAPI, users []childParent, acct *account, st *store.Store, scanID string) (int, int, error) {
	var idless skipTally
	total, inserted, err := childFanOut(ctx, st, acct, "global", "iam:ListSigningCertificates", users, isIAMUserGone,
		func(gctx context.Context, u childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := iam.NewListSigningCertificatesPaginator(client, &iam.ListSigningCertificatesInput{UserName: &u.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, c := range out.Certificates {
					id := sv(c.CertificateId)
					if id == "" {
						idless.skipped(u.arn, nil)
						continue
					}
					rows = append(rows, iamUserChildRow(acct, scanID, TypeIAMSigningCertificate, u.arn, "signing-certificate", id, c.Status, c.UploadDate, c))
				}
			}
			return rows, nil
		})
	warnNotStored(st, "iam:ListSigningCertificates", acct, "global", &idless, "signing certificates have no CertificateId")
	return total, inserted, err
}

// scanIAMSSHPublicKeys lists each user's SSH public keys (CodeCommit). The list
// element is stored as is: GetSSHPublicKey would add the key body and
// fingerprint at one more call per key against IAM's low TPS ceiling, and
// neither is needed to inventory the key or link it to its user.
func scanIAMSSHPublicKeys(ctx context.Context, client iamAPI, users []childParent, acct *account, st *store.Store, scanID string) (int, int, error) {
	var idless skipTally
	total, inserted, err := childFanOut(ctx, st, acct, "global", "iam:ListSSHPublicKeys", users, isIAMUserGone,
		func(gctx context.Context, u childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := iam.NewListSSHPublicKeysPaginator(client, &iam.ListSSHPublicKeysInput{UserName: &u.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, k := range out.SSHPublicKeys {
					id := sv(k.SSHPublicKeyId)
					if id == "" {
						idless.skipped(u.arn, nil)
						continue
					}
					rows = append(rows, iamUserChildRow(acct, scanID, TypeIAMSSHPublicKey, u.arn, "ssh-public-key", id, k.Status, k.UploadDate, k))
				}
			}
			return rows, nil
		})
	warnNotStored(st, "iam:ListSSHPublicKeys", acct, "global", &idless, "SSH public keys have no SSHPublicKeyId")
	return total, inserted, err
}

// iamDelegationRequestNativeID builds the delegation-request ARN in the format
// the IAM Service Reference documents
// (arn:{p}:iam::{account}:delegation-request/{id}); the API returns no ARN.
// The account slot is the scanned account, the one whose listing returned the
// request. Unverified live: AWS may fill that slot with the requestor's
// (partner's) account (RequestorId) instead, which is plausible because the
// partner calls CreateDelegationRequest and so creates the resource. The SDK
// DelegationRequest also carries OwnerAccountId, the likeliest true value. The
// scanned account is still used because a type takes one fixed NativeID
// source, applied to every row, and the model does not mark OwnerAccountId
// @required, so a row without it would need a fallback or be dropped.
// Neither candidate can be verified here.
func iamDelegationRequestNativeID(partition, accountID, id string) string {
	return arnFor(partition, "iam", "", accountID, "delegation-request/"+id)
}

// scanIAMDelegationRequests stores what an unfiltered ListDelegationRequests
// returns: delegation requests, each a partner's request for temporary
// delegated access. The SDK documents one exclusion: a request assigned to the
// account but with no owner is not in that account's output. Unverified live:
// that the unfiltered call (no OwnerId) returns every request the account owns.
//
// The list element is the DelegationRequest shape GetDelegationRequest also
// returns; Get adds only a permission check relative to the caller, so no
// per-request Get is made. GetHumanReadableSummary is not called: an LLM
// generates its text asynchronously, so the text describes the request and is
// not state of it.
func scanIAMDelegationRequests(ctx context.Context, client iamAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	const op = "iam:ListDelegationRequests"
	// The model does not mark DelegationRequestId required, and a row cannot be
	// keyed without it, so such requests are counted and warned about once.
	idless := 0
	defer func() {
		if idless > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: op, Scope: acct.ID,
				Message: fmt.Sprintf("delegation requests listed without a DelegationRequestId: %d, not stored, because disco keys each row by that id; report it if it recurs", idless),
			})
		}
	}()
	input := &iam.ListDelegationRequestsInput{MaxItems: sdkaws.Int32(1000)}
	for {
		out, err := client.ListDelegationRequests(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, op, acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("%s: %w", op, err)
		}
		var batch []*store.Resource
		for _, r := range out.DelegationRequests {
			id := sv(r.DelegationRequestId)
			if id == "" {
				idless++
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeIAMDelegationRequest,
				NativeID:       iamDelegationRequestNativeID(acct.partition(), acct.ID, id),
				Name:           &id,
				Status:         nonEmptyPtr(string(r.State)),
				CreatedAt:      tp(r.CreateDate),
				AttributesJSON: mustJSON(r),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert IAM delegation requests: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		next := sv(out.Marker)
		if next == "" {
			return total, inserted, nil
		}
		if next == sv(input.Marker) {
			return total, inserted, fmt.Errorf("%s: Marker %q: %w", op, next, errRepeatedNextToken)
		}
		input.Marker = out.Marker
	}
}
