package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
)

// scanSSOExtended discovers per-instance applications, per-application
// assignments, and the per-instance access-control attribute configuration
// (singleton). All three CFN types live under aws:sso:*.
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

	for _, inst := range instances {
		instArn := sv(inst.InstanceArn)
		if instArn == "" {
			continue
		}
		appARNs, t, i, ferr := scanSSOApplications(ctx, client, acct, region, st, scanID, instArn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		for _, aa := range appARNs {
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
	return total, inserted, nil
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
				Name: name, Region: &region, ManagedByProvider: true,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
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
