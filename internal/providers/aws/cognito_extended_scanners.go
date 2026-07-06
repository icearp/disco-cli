package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
)

// scanCognitoExtended adds 9 user-pool / identity-pool sub-resource types
// via per-pool fan-outs. UserPoolUser and UserPoolUserToGroupAttachment
// deferred (potential cardinality explosion); ManagedLoginBranding has
// no list op; IdentityPoolPrincipalTag requires per-(pool, IdP) iteration.
func scanCognitoExtended(ctx context.Context, idp cognitoidpAPI, id cognitoidentityAPI, acct *account, region string, st *store.Store, scanID string, poolIDs, idPoolIDs []string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanCogUserPoolDomains(ctx, idp, acct, region, st, scanID, poolIDs) },
		func() (int, int, error) { return scanCogGroups(ctx, idp, acct, region, st, scanID, poolIDs) },
		func() (int, int, error) { return scanCogIdentityProviders(ctx, idp, acct, region, st, scanID, poolIDs) },
		func() (int, int, error) { return scanCogResourceServers(ctx, idp, acct, region, st, scanID, poolIDs) },
		func() (int, int, error) { return scanCogRiskConfig(ctx, idp, acct, region, st, scanID, poolIDs) },
		func() (int, int, error) { return scanCogUICustomization(ctx, idp, acct, region, st, scanID, poolIDs) },
		func() (int, int, error) { return scanCogLogDelivery(ctx, idp, acct, region, st, scanID, poolIDs) },
		func() (int, int, error) { return scanCogTerms(ctx, idp, acct, region, st, scanID, poolIDs) },
		func() (int, int, error) {
			return scanCogIDPoolRoleAttachments(ctx, id, acct, region, st, scanID, idPoolIDs)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func cogUPARN(region, acct, poolID, kind, id string) string {
	return fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s/%s/%s", region, acct, poolID, kind, id)
}

// scanCogUserPoolDomains: per-pool DescribeUserPool exposes the Domain
// field; if non-empty, DescribeUserPoolDomain returns the description.
func scanCogUserPoolDomains(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, pid := range poolIDs {
		id := pid
		dpOut, err := client.DescribeUserPool(ctx, &cognitoidp.DescribeUserPoolInput{UserPoolId: &id})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("cognito-idp:DescribeUserPool %s: %w", pid, err)
		}
		if dpOut.UserPool == nil || dpOut.UserPool.Domain == nil || *dpOut.UserPool.Domain == "" {
			continue
		}
		domain := *dpOut.UserPool.Domain
		out, derr := client.DescribeUserPoolDomain(ctx, &cognitoidp.DescribeUserPoolDomainInput{Domain: &domain})
		if derr != nil {
			if isAccessDenied(derr) {
				continue
			}
			return 0, 0, fmt.Errorf("cognito-idp:DescribeUserPoolDomain %s: %w", domain, derr)
		}
		if out.DomainDescription == nil {
			continue
		}
		arn := cogUPARN(region, acct.ID, pid, "domain", domain)
		label := domain
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCognitoUserPoolDomain, NativeID: arn,
			Name: &label, Region: &region, AttributesJSON: mustJSON(out.DomainDescription), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "cognito user-pool-domains")
}

func scanCogGroups(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, pid := range poolIDs {
		id := pid
		pager := cognitoidp.NewListGroupsPaginator(client, &cognitoidp.ListGroupsInput{UserPoolId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("cognito-idp:ListGroups %s: %w", pid, perr)
			}
			for _, g := range out.Groups {
				name := sv(g.GroupName)
				if name == "" {
					continue
				}
				arn := cogUPARN(region, acct.ID, pid, "group", name)
				label := name
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCognitoUserPoolGroup, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "cognito user-pool-groups")
}

func scanCogIdentityProviders(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, pid := range poolIDs {
		id := pid
		pager := cognitoidp.NewListIdentityProvidersPaginator(client, &cognitoidp.ListIdentityProvidersInput{UserPoolId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("cognito-idp:ListIdentityProviders %s: %w", pid, perr)
			}
			for _, p := range out.Providers {
				name := sv(p.ProviderName)
				if name == "" {
					continue
				}
				arn := cogUPARN(region, acct.ID, pid, "identityprovider", name)
				label := name
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCognitoUserPoolIdentityProvider, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "cognito user-pool-identity-providers")
}

func scanCogResourceServers(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, pid := range poolIDs {
		id := pid
		pager := cognitoidp.NewListResourceServersPaginator(client, &cognitoidp.ListResourceServersInput{UserPoolId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("cognito-idp:ListResourceServers %s: %w", pid, perr)
			}
			for _, r := range out.ResourceServers {
				ident := sv(r.Identifier)
				if ident == "" {
					continue
				}
				arn := cogUPARN(region, acct.ID, pid, "resourceserver", ident)
				label := sv(r.Name)
				if label == "" {
					label = ident
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCognitoUserPoolResourceServer, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "cognito user-pool-resource-servers")
}

// scanCogRiskConfig — per-pool singleton; tolerates ResourceNotFound when
// risk config isn't enabled for the pool.
func scanCogRiskConfig(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, pid := range poolIDs {
		id := pid
		out, err := client.DescribeRiskConfiguration(ctx, &cognitoidp.DescribeRiskConfigurationInput{UserPoolId: &id})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("cognito-idp:DescribeRiskConfiguration %s: %w", pid, err)
		}
		if out.RiskConfiguration == nil {
			continue
		}
		arn := cogUPARN(region, acct.ID, pid, "riskconfiguration", "_")
		name := "risk-configuration"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCognitoUserPoolRiskConfigurationAttachment, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(out.RiskConfiguration), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "cognito user-pool-risk-configurations")
}

// scanCogUICustomization — per-pool singleton; GetUICustomization returns
// empty UICustomizationType if no customization configured (skip).
func scanCogUICustomization(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, pid := range poolIDs {
		id := pid
		out, err := client.GetUICustomization(ctx, &cognitoidp.GetUICustomizationInput{UserPoolId: &id})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("cognito-idp:GetUICustomization %s: %w", pid, err)
		}
		if out.UICustomization == nil || sv(out.UICustomization.UserPoolId) == "" {
			continue
		}
		arn := cogUPARN(region, acct.ID, pid, "ui-customization", "_")
		name := "ui-customization"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCognitoUserPoolUICustomizationAttachment, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(out.UICustomization), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "cognito user-pool-ui-customizations")
}

func scanCogLogDelivery(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, pid := range poolIDs {
		id := pid
		out, err := client.GetLogDeliveryConfiguration(ctx, &cognitoidp.GetLogDeliveryConfigurationInput{UserPoolId: &id})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("cognito-idp:GetLogDeliveryConfiguration %s: %w", pid, err)
		}
		if out.LogDeliveryConfiguration == nil {
			continue
		}
		arn := cogUPARN(region, acct.ID, pid, "log-delivery", "_")
		name := "log-delivery"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCognitoLogDeliveryConfiguration, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(out.LogDeliveryConfiguration), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "cognito log-delivery-configurations")
}

func scanCogTerms(ctx context.Context, client cognitoidpAPI, acct *account, region string, st *store.Store, scanID string, poolIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, pid := range poolIDs {
		id := pid
		var token *string
		for {
			out, err := client.ListTerms(ctx, &cognitoidp.ListTermsInput{UserPoolId: &id, NextToken: token})
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return 0, 0, fmt.Errorf("cognito-idp:ListTerms %s: %w", pid, err)
			}
			for _, t := range out.Terms {
				tid := sv(t.TermsId)
				if tid == "" {
					continue
				}
				arn := cogUPARN(region, acct.ID, pid, "terms", tid)
				label := sv(t.TermsName)
				if label == "" {
					label = tid
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCognitoTerms, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertBatch(st, batch, "cognito terms")
}

// scanCogIDPoolRoleAttachments — one row per identity pool keyed on the
// pool ARN with the role mappings as attributes. Distinct type from
// identity-pool itself; CFN models RoleAttachment separately.
func scanCogIDPoolRoleAttachments(ctx context.Context, client cognitoidentityAPI, acct *account, region string, st *store.Store, scanID string, idPoolIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, pid := range idPoolIDs {
		id := pid
		out, err := client.GetIdentityPoolRoles(ctx, &cognitoidentity.GetIdentityPoolRolesInput{IdentityPoolId: &id})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("cognito-identity:GetIdentityPoolRoles %s: %w", pid, err)
		}
		if len(out.Roles) == 0 && len(out.RoleMappings) == 0 {
			continue
		}
		arn := fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/%s/roleattachment", region, acct.ID, pid)
		name := "role-attachment"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCognitoIdentityPoolRoleAttachment, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "cognito identity-pool-role-attachments")
}
