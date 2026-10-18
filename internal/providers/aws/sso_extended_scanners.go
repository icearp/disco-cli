package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
	"github.com/icearp/disco-cli/store"
)

// scanSSOExtended discovers per-instance applications, per-application
// assignments, and the per-instance access-control attribute configuration
// (singleton), then the per-instance Regions and the per-application access
// scopes, authentication methods and grants. Those last phases run after every
// other one so a failure in them never costs the established rows.
func scanSSOExtended(ctx context.Context, client ssoadminAPI, acct *account, region string, instances []ssotypes.InstanceMetadata, st *store.Store, scanID string) (total, inserted int, err error) {
	// Application providers are account-wide (not per-instance) — the AWS-managed
	// catalog of federation providers available to the account.
	{
		t, i, ferr := scanSSOApplicationProviders(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	var instParents, appParents []childParent
	for _, inst := range instances {
		instArn := sv(inst.InstanceArn)
		if instArn == "" {
			continue
		}
		instParents = append(instParents, childParent{id: instArn, arn: instArn})
		appARNs, t, i, ferr := scanSSOApplications(ctx, client, acct, region, st, scanID, instArn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		for _, aa := range appARNs {
			appParents = append(appParents, childParent{id: aa, arn: aa})
			t, i, ferr = scanSSOApplicationAssignments(ctx, client, acct, region, st, scanID, aa)
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
		}

		t, i, ferr = scanSSOInstanceAccessControlAttributeConfig(ctx, client, acct, region, st, scanID, instArn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanSSOTrustedTokenIssuers(ctx, client, acct, region, st, scanID, instArn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	for _, scan := range []func() (int, int, error){
		func() (int, int, error) { return scanSSORegions(ctx, client, acct, region, st, scanID, instParents) },
		func() (int, int, error) {
			return scanSSOApplicationAccessScopes(ctx, client, acct, region, st, scanID, appParents)
		},
		func() (int, int, error) {
			return scanSSOApplicationAuthenticationMethods(ctx, client, acct, region, st, scanID, appParents)
		},
		func() (int, int, error) {
			return scanSSOApplicationGrants(ctx, client, acct, region, st, scanID, appParents)
		},
	} {
		t, i, ferr := scan()
		total += t
		inserted += i
		if ferr != nil {
			return total, inserted, ferr
		}
	}
	return total, inserted, nil
}

// isSSOParentGone reports a parent (an application or a permission set)
// deleted between the call that listed it and the per-parent list call.
func isSSOParentGone(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

const (
	ssoListManagedPoliciesOp          = "ssoadmin:ListManagedPoliciesInPermissionSet"
	ssoListCustomerManagedPolicyRefOp = "ssoadmin:ListCustomerManagedPolicyReferencesInPermissionSet"
)

// scanSSOPermissionSetPolicies lists the policies attached to each permission
// set: one row per AWS managed policy and one per customer managed policy
// reference, contained by the permission set. They are rows of their own, not
// permission-set attributes, so attaching or detaching a policy changes that
// one row instead of version-splitting the permission set. Both lists run;
// a transient failure in one is demoted so it cannot hide the other's error.
func scanSSOPermissionSetPolicies(ctx context.Context, client ssoadminAPI, acct *account, region string, sets []ssoPermissionSet, st *store.Store, scanID string) (int, int, error) {
	parents := make([]childParent, 0, len(sets))
	instanceOf := make(map[string]string, len(sets))
	for _, ps := range sets {
		parents = append(parents, childParent{id: ps.arn, arn: ps.arn})
		instanceOf[ps.arn] = ps.instanceARN
	}
	mt, mi, merr := scanSSOManagedPolicies(ctx, client, acct, region, st, scanID, parents, instanceOf)
	ct, ci, cerr := scanSSOCustomerManagedPolicyReferences(ctx, client, acct, region, st, scanID, parents, instanceOf)
	return mt + ct, mi + ci, errors.Join(
		demoteTransient(st, ssoListManagedPoliciesOp, acct, region, merr),
		demoteTransient(st, ssoListCustomerManagedPolicyRefOp, acct, region, cerr),
	)
}

// ssoPolicyPageSize is the documented MaxResults cap of both policy list ops.
const ssoPolicyPageSize = 100

// scanSSOManagedPolicies lists each permission set's AWS managed policies. An
// attachment has no ARN of its own; it is keyed
// {permissionSetArn}/managed-policy/{policyArn}.
func scanSSOManagedPolicies(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, sets []childParent, instanceOf map[string]string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, ssoListManagedPoliciesOp, sets, isSSOParentGone,
		func(ctx context.Context, ps childParent) ([]*store.Resource, error) {
			instanceARN := instanceOf[ps.id]
			var rows []*store.Resource
			p := ssoadmin.NewListManagedPoliciesInPermissionSetPaginator(client,
				&ssoadmin.ListManagedPoliciesInPermissionSetInput{InstanceArn: &instanceARN, PermissionSetArn: &ps.id},
				func(o *ssoadmin.ListManagedPoliciesInPermissionSetPaginatorOptions) { o.Limit = ssoPolicyPageSize })
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, mp := range page.AttachedManagedPolicies {
					policyARN := sv(mp.Arn)
					if policyARN == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeSSOManagedPolicy, NativeID: ps.arn + "/managed-policy/" + policyARN,
						Name: mp.Name, Region: &region, AttributesJSON: mustJSON(mp), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanSSOCustomerManagedPolicyReferences lists each permission set's customer
// managed policy references. A reference names an IAM policy by path and name
// (the policy itself lives in each account the permission set is provisioned
// to), so it is keyed {permissionSetArn}/customer-managed-policy{path}{name},
// the path and name the policy's IAM ARN is built from.
func scanSSOCustomerManagedPolicyReferences(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, sets []childParent, instanceOf map[string]string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, ssoListCustomerManagedPolicyRefOp, sets, isSSOParentGone,
		func(ctx context.Context, ps childParent) ([]*store.Resource, error) {
			instanceARN := instanceOf[ps.id]
			var rows []*store.Resource
			p := ssoadmin.NewListCustomerManagedPolicyReferencesInPermissionSetPaginator(client,
				&ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetInput{InstanceArn: &instanceARN, PermissionSetArn: &ps.id},
				func(o *ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetPaginatorOptions) {
					o.Limit = ssoPolicyPageSize
				})
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, ref := range page.CustomerManagedPolicyReferences {
					name := sv(ref.Name)
					if name == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeSSOCustomerManagedPolicyReference, NativeID: ps.arn + "/customer-managed-policy" + ssoPolicyPath(ref.Path) + name,
						Name: &name, Region: &region, AttributesJSON: mustJSON(ref), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// ssoPolicyPath is a reference's IAM path. The model makes Path optional and
// documents "/" as its default, so an absent path is that same "/" and the
// policy keeps one key whichever form the API returns.
func ssoPolicyPath(path *string) string {
	if p := sv(path); p != "" {
		return p
	}
	return "/"
}

// scanSSOApplicationAccessScopes lists each application's access scopes. A
// scope has no ARN; it is keyed {applicationArn}/access-scope/{scope}.
func scanSSOApplicationAccessScopes(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "ssoadmin:ListApplicationAccessScopes", apps, isSSOParentGone,
		func(ctx context.Context, app childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			p := ssoadmin.NewListApplicationAccessScopesPaginator(client, &ssoadmin.ListApplicationAccessScopesInput{ApplicationArn: &app.id})
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, sc := range page.Scopes {
					scope := sv(sc.Scope)
					if scope == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeSSOApplicationAccessScope, NativeID: app.arn + "/access-scope/" + scope,
						Name: &scope, Region: &region, AttributesJSON: mustJSON(sc), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanSSOApplicationAuthenticationMethods lists each application's
// authentication methods, at most one per method type, so a method is keyed
// {applicationArn}/authentication-method/{type}.
func scanSSOApplicationAuthenticationMethods(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "ssoadmin:ListApplicationAuthenticationMethods", apps, isSSOParentGone,
		func(ctx context.Context, app childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			p := ssoadmin.NewListApplicationAuthenticationMethodsPaginator(client, &ssoadmin.ListApplicationAuthenticationMethodsInput{ApplicationArn: &app.id})
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, m := range page.AuthenticationMethods {
					methodType := string(m.AuthenticationMethodType)
					if methodType == "" {
						continue
					}
					attrs, err := ssoAuthenticationMethodAttrsJSON(m)
					if err != nil {
						return nil, fmt.Errorf("authentication method %s: %w", methodType, err)
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeSSOApplicationAuthenticationMethod, NativeID: app.arn + "/authentication-method/" + methodType,
						Name: &methodType, Region: &region, AttributesJSON: attrs, DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// ssoAuthenticationMethodAttrsJSON renders the item in its SDK shape. The IAM
// method's ActorPolicy is a Smithy document, which encoding/json renders as {}
// (its value is unexported), so the policy is written out through the
// document's own marshaler instead.
func ssoAuthenticationMethodAttrsJSON(m ssotypes.AuthenticationMethodItem) (string, error) {
	iam, ok := m.AuthenticationMethod.(*ssotypes.AuthenticationMethodMemberIam)
	if !ok || iam.Value.ActorPolicy == nil {
		return mustJSON(m), nil
	}
	policy, err := iam.Value.ActorPolicy.MarshalSmithyDocument()
	if err != nil {
		return "", fmt.Errorf("marshal ActorPolicy: %w", err)
	}
	type iamMethod struct {
		ActorPolicy json.RawMessage `json:"ActorPolicy"`
	}
	return mustJSON(struct {
		AuthenticationMethod     struct{ Value iamMethod }
		AuthenticationMethodType ssotypes.AuthenticationMethodType
	}{
		AuthenticationMethod:     struct{ Value iamMethod }{Value: iamMethod{ActorPolicy: policy}},
		AuthenticationMethodType: m.AuthenticationMethodType,
	}), nil
}

// scanSSOApplicationGrants lists each application's grants, at most one per
// grant type, so a grant is keyed {applicationArn}/grant/{type}. No grant type
// carries a credential: JWT bearer grants name trusted token issuer ARNs and
// audiences, authorization-code grants redirect URIs.
func scanSSOApplicationGrants(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "ssoadmin:ListApplicationGrants", apps, isSSOParentGone,
		func(ctx context.Context, app childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			p := ssoadmin.NewListApplicationGrantsPaginator(client, &ssoadmin.ListApplicationGrantsInput{ApplicationArn: &app.id})
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, g := range page.Grants {
					grantType := string(g.GrantType)
					if grantType == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeSSOApplicationGrant, NativeID: app.arn + "/grant/" + grantType,
						Name: &grantType, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanSSORegions lists each instance's enabled Regions (the primary plus any
// multi-Region replicas). A Region is keyed {instanceArn}/region/{regionName}.
//
// Multi-Region needs an organization instance, so a ValidationException
// (modeled for ListRegions) skips that instance, as the org-only ABAC config
// does in scanSSOInstanceAccessControlAttributeConfig. The live error shape
// for an account instance is unverified.
func scanSSORegions(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, instances []childParent) (int, int, error) {
	isNotOrgInstance := func(err error) bool { return isAPIErrorCode(err, "ValidationException") }
	return childFanOut(ctx, st, acct, region, "ssoadmin:ListRegions", instances, isNotOrgInstance,
		func(ctx context.Context, inst childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			p := ssoadmin.NewListRegionsPaginator(client, &ssoadmin.ListRegionsInput{InstanceArn: &inst.id})
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, rm := range page.Regions {
					name := sv(rm.RegionName)
					if name == "" {
						continue
					}
					r := &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeSSORegion, NativeID: inst.arn + "/region/" + name,
						Name: &name, Region: &region, CreatedAt: tp(rm.AddedDate),
						AttributesJSON: mustJSON(rm), DiscoveredBy: scanID,
					}
					if rm.Status != "" {
						status := string(rm.Status)
						r.Status = &status
					}
					rows = append(rows, r)
				}
			}
			return rows, nil
		})
}

// scanSSOApplicationProviders captures the AWS-managed catalog of application
// (federation) providers. Flagged ManagedByProvider: AWS-owned catalog
// entries, not user-created resources.
func scanSSOApplicationProviders(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ssoadmin.NewListApplicationProvidersPaginator(client, &ssoadmin.ListApplicationProvidersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssoadmin:ListApplicationProviders", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssoadmin:ListApplicationProviders: %w", err)
		}
		for _, p := range out.ApplicationProviders {
			arn := sv(p.ApplicationProviderArn)
			if arn == "" {
				continue
			}
			var name *string
			if p.DisplayData != nil {
				name = p.DisplayData.DisplayName
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSOApplicationProvider, NativeID: arn,
				Name: name, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "sso application-providers")
}

// scanSSOTrustedTokenIssuers captures per-instance trusted token issuers.
// The resolver wires each to its parent instance (RelAttachedTo); the parent
// ARN is embedded as InstanceArn since issuer metadata carries no back-reference.
func scanSSOTrustedTokenIssuers(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, instanceARN string) (int, int, error) {
	ia := instanceARN
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListTrustedTokenIssuers(ctx, &ssoadmin.ListTrustedTokenIssuersInput{InstanceArn: &ia, NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssoadmin:ListTrustedTokenIssuers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssoadmin:ListTrustedTokenIssuers: %w", err)
		}
		for _, t := range out.TrustedTokenIssuers {
			arn := sv(t.TrustedTokenIssuerArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSOTrustedTokenIssuer, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(ssoTrustedTokenIssuerAttrs{TrustedTokenIssuerMetadata: t, InstanceArn: ia}),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "sso trusted-token-issuers")
}

// ssoTrustedTokenIssuerAttrs embeds the SDK issuer metadata plus the parent
// instance ARN (absent from the metadata); the resolver reads InstanceArn to
// wire the attached-to edge.
type ssoTrustedTokenIssuerAttrs struct {
	ssotypes.TrustedTokenIssuerMetadata
	InstanceArn string `json:"InstanceArn,omitempty"`
}

func scanSSOApplications(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, instanceARN string) ([]string, int, int, error) {
	ia := instanceARN
	var batch []*store.Resource
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListApplications(ctx, &ssoadmin.ListApplicationsInput{InstanceArn: &ia, NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "ssoadmin:ListApplications", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("ssoadmin:ListApplications: %w", err)
		}
		for _, a := range out.Applications {
			arn := sv(a.ApplicationArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSOApplication, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "sso applications")
	return arns, t, i, err
}

// scanSSOApplicationAssignments synthesizes ARN: {appArn}/assignment/{principalType}/{principalId}.
func scanSSOApplicationAssignments(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, applicationARN string) (int, int, error) {
	aa := applicationARN
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListApplicationAssignments(ctx, &ssoadmin.ListApplicationAssignmentsInput{ApplicationArn: &aa, NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssoadmin:ListApplicationAssignments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssoadmin:ListApplicationAssignments: %w", err)
		}
		for _, a := range out.ApplicationAssignments {
			pid := sv(a.PrincipalId)
			ptype := string(a.PrincipalType)
			if pid == "" {
				continue
			}
			arn := fmt.Sprintf("%s/assignment/%s/%s", aa, ptype, pid)
			label := pid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSOApplicationAssignment, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "sso application-assignments")
}

// scanSSOInstanceAccessControlAttributeConfig captures the per-instance
// singleton attribute config. Synth ARN: {instanceArn}/access-control-attribute-configuration.
func scanSSOInstanceAccessControlAttributeConfig(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, instanceARN string) (int, int, error) {
	ia := instanceARN
	out, err := client.DescribeInstanceAccessControlAttributeConfiguration(ctx, &ssoadmin.DescribeInstanceAccessControlAttributeConfigurationInput{InstanceArn: &ia})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("ssoadmin:DescribeInstanceAccessControlAttributeConfiguration: %w", err)
	}
	if out.InstanceAccessControlAttributeConfiguration == nil && string(out.Status) == "" {
		return 0, 0, nil
	}
	arn := ia + "/access-control-attribute-configuration"
	label := "access-control-attributes"
	status := string(out.Status)
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeSSOInstanceAccessControlAttributeConfiguration, NativeID: arn,
		Name: &label, Region: &region, Status: &status,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "sso instance-access-control-attribute-config")
}
