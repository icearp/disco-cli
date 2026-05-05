package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTCertificate},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTCACertificate},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTCertificateProvider},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTPolicy},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTPolicyPrincipalAttachment},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTRoleAlias},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTAuthorizer},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTDomainConfiguration},
	)
}

// iotCertsAPI is the narrow surface used by the Certs/Auth family.
type iotCertsAPI interface {
	ListCertificates(context.Context, *iot.ListCertificatesInput, ...func(*iot.Options)) (*iot.ListCertificatesOutput, error)
	DescribeCertificate(context.Context, *iot.DescribeCertificateInput, ...func(*iot.Options)) (*iot.DescribeCertificateOutput, error)
	ListCACertificates(context.Context, *iot.ListCACertificatesInput, ...func(*iot.Options)) (*iot.ListCACertificatesOutput, error)
	DescribeCACertificate(context.Context, *iot.DescribeCACertificateInput, ...func(*iot.Options)) (*iot.DescribeCACertificateOutput, error)
	ListCertificateProviders(context.Context, *iot.ListCertificateProvidersInput, ...func(*iot.Options)) (*iot.ListCertificateProvidersOutput, error)
	DescribeCertificateProvider(context.Context, *iot.DescribeCertificateProviderInput, ...func(*iot.Options)) (*iot.DescribeCertificateProviderOutput, error)
	ListPolicies(context.Context, *iot.ListPoliciesInput, ...func(*iot.Options)) (*iot.ListPoliciesOutput, error)
	GetPolicy(context.Context, *iot.GetPolicyInput, ...func(*iot.Options)) (*iot.GetPolicyOutput, error)
	ListPolicyPrincipals(context.Context, *iot.ListPolicyPrincipalsInput, ...func(*iot.Options)) (*iot.ListPolicyPrincipalsOutput, error)
	ListRoleAliases(context.Context, *iot.ListRoleAliasesInput, ...func(*iot.Options)) (*iot.ListRoleAliasesOutput, error)
	DescribeRoleAlias(context.Context, *iot.DescribeRoleAliasInput, ...func(*iot.Options)) (*iot.DescribeRoleAliasOutput, error)
	ListAuthorizers(context.Context, *iot.ListAuthorizersInput, ...func(*iot.Options)) (*iot.ListAuthorizersOutput, error)
	DescribeAuthorizer(context.Context, *iot.DescribeAuthorizerInput, ...func(*iot.Options)) (*iot.DescribeAuthorizerOutput, error)
	ListDomainConfigurations(context.Context, *iot.ListDomainConfigurationsInput, ...func(*iot.Options)) (*iot.ListDomainConfigurationsOutput, error)
	DescribeDomainConfiguration(context.Context, *iot.DescribeDomainConfigurationInput, ...func(*iot.Options)) (*iot.DescribeDomainConfigurationOutput, error)
}

// scanIoTCerts runs Certs/Auth-family phases.
func scanIoTCerts(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIoTCertificates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTCACertificates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTCertificateProviders(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTPolicies(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanIoTPolicyPrincipalAttachments(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanIoTRoleAliases(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTAuthorizers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTDomainConfigurations(ctx, client, acct, region, st, scanID) },
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

func scanIoTCertificates(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListCertificatesPaginator(client, &iot.ListCertificatesInput{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListCertificates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListCertificates: %w", perr)
		}
		for _, c := range out.Certificates {
			if c.CertificateId != nil {
				ids = append(ids, *c.CertificateId)
			}
		}
	}
	return iotDescribeFanout(ctx, ids, fanoutMed, func(gctx context.Context, id string) (*store.Resource, error) {
		out, derr := client.DescribeCertificate(gctx, &iot.DescribeCertificateInput{CertificateId: &id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeCertificate %s: %w", id, derr)
		}
		if out.CertificateDescription == nil {
			return nil, nil
		}
		arn := sv(out.CertificateDescription.CertificateArn)
		if arn == "" {
			return nil, nil
		}
		cid := sv(out.CertificateDescription.CertificateId)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIoTCertificate,
			NativeID:       arn,
			Name:           &cid,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "iot certificates")
}

func scanIoTCACertificates(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListCACertificatesPaginator(client, &iot.ListCACertificatesInput{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListCACertificates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListCACertificates: %w", perr)
		}
		for _, c := range out.Certificates {
			if c.CertificateId != nil {
				ids = append(ids, *c.CertificateId)
			}
		}
	}
	return iotDescribeFanout(ctx, ids, fanoutMed, func(gctx context.Context, id string) (*store.Resource, error) {
		out, derr := client.DescribeCACertificate(gctx, &iot.DescribeCACertificateInput{CertificateId: &id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeCACertificate %s: %w", id, derr)
		}
		if out.CertificateDescription == nil {
			return nil, nil
		}
		arn := sv(out.CertificateDescription.CertificateArn)
		if arn == "" {
			return nil, nil
		}
		cid := sv(out.CertificateDescription.CertificateId)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIoTCACertificate,
			NativeID:       arn,
			Name:           &cid,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "iot ca certificates")
}

// scanIoTCertificateProviders — SDK has no paginator. Manual NextToken loop.
func scanIoTCertificateProviders(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var names []string
	var token *string
	for {
		out, perr := client.ListCertificateProviders(ctx, &iot.ListCertificateProvidersInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListCertificateProviders", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListCertificateProviders: %w", perr)
		}
		for _, p := range out.CertificateProviders {
			if p.CertificateProviderName != nil {
				names = append(names, *p.CertificateProviderName)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeCertificateProvider(gctx, &iot.DescribeCertificateProviderInput{CertificateProviderName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeCertificateProvider %s: %w", name, derr)
		}
		arn := sv(out.CertificateProviderArn)
		if arn == "" {
			return nil, nil
		}
		pname := sv(out.CertificateProviderName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIoTCertificateProvider,
			NativeID:       arn,
			Name:           &pname,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "iot certificate providers")
}

func scanIoTPolicies(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListPoliciesPaginator(client, &iot.ListPoliciesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListPolicies: %w", perr)
		}
		for _, p := range out.Policies {
			if p.PolicyName != nil {
				names = append(names, *p.PolicyName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.GetPolicy(gctx, &iot.GetPolicyInput{PolicyName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:GetPolicy %s: %w", name, derr)
		}
		arn := sv(out.PolicyArn)
		if arn == "" {
			return nil, nil
		}
		pname := sv(out.PolicyName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIoTPolicy,
			NativeID:       arn,
			Name:           &pname,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "iot policies")
}

// scanIoTPolicyPrincipalAttachments emits one row per (Policy, Principal)
// pair via per-policy fan-out of ListPolicyPrincipals. NativeID synthesized
// since the attachment has no AWS-issued ARN.
func scanIoTPolicyPrincipalAttachments(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListPoliciesPaginator(client, &iot.ListPoliciesInput{})
	var policies []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListPolicies: %w", perr)
		}
		for _, p := range out.Policies {
			if p.PolicyName != nil {
				policies = append(policies, *p.PolicyName)
			}
		}
	}
	if len(policies) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range policies {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			pp := iot.NewListPolicyPrincipalsPaginator(client, &iot.ListPolicyPrincipalsInput{PolicyName: &name})
			for pp.HasMorePages() {
				out, perr := pp.NextPage(gctx)
				if perr != nil {
					if isAccessDenied(perr) {
						return nil
					}
					return fmt.Errorf("iot:ListPolicyPrincipals %s: %w", name, perr)
				}
				for _, p := range out.Principals {
					principal := p
					arn := fmt.Sprintf("arn:aws:iot:%s:%s:policy/%s/principal/%s", region, acct.ID, name, principal)
					attrs := map[string]string{"PolicyName": name, "Principal": principal}
					mu.Lock()
					batch = append(batch, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeIoTPolicyPrincipalAttachment,
						NativeID:       arn,
						Name:           &principal,
						Region:         &region,
						AttributesJSON: mustJSON(attrs),
						DiscoveredBy:   scanID,
					})
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert iot policy-principal-attachments: %w", uerr)
	}
	return len(batch), n, nil
}

func scanIoTRoleAliases(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListRoleAliasesPaginator(client, &iot.ListRoleAliasesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListRoleAliases", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListRoleAliases: %w", perr)
		}
		names = append(names, out.RoleAliases...)
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeRoleAlias(gctx, &iot.DescribeRoleAliasInput{RoleAlias: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeRoleAlias %s: %w", name, derr)
		}
		if out.RoleAliasDescription == nil {
			return nil, nil
		}
		arn := sv(out.RoleAliasDescription.RoleAliasArn)
		if arn == "" {
			return nil, nil
		}
		alias := sv(out.RoleAliasDescription.RoleAlias)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIoTRoleAlias,
			NativeID:       arn,
			Name:           &alias,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "iot role aliases")
}

func scanIoTAuthorizers(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListAuthorizersPaginator(client, &iot.ListAuthorizersInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListAuthorizers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListAuthorizers: %w", perr)
		}
		for _, a := range out.Authorizers {
			if a.AuthorizerName != nil {
				names = append(names, *a.AuthorizerName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeAuthorizer(gctx, &iot.DescribeAuthorizerInput{AuthorizerName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeAuthorizer %s: %w", name, derr)
		}
		if out.AuthorizerDescription == nil {
			return nil, nil
		}
		arn := sv(out.AuthorizerDescription.AuthorizerArn)
		if arn == "" {
			return nil, nil
		}
		aname := sv(out.AuthorizerDescription.AuthorizerName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIoTAuthorizer,
			NativeID:       arn,
			Name:           &aname,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "iot authorizers")
}

func scanIoTDomainConfigurations(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListDomainConfigurationsPaginator(client, &iot.ListDomainConfigurationsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListDomainConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListDomainConfigurations: %w", perr)
		}
		for _, d := range out.DomainConfigurations {
			if d.DomainConfigurationName != nil {
				names = append(names, *d.DomainConfigurationName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeDomainConfiguration(gctx, &iot.DescribeDomainConfigurationInput{DomainConfigurationName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeDomainConfiguration %s: %w", name, derr)
		}
		arn := sv(out.DomainConfigurationArn)
		if arn == "" {
			return nil, nil
		}
		dname := sv(out.DomainConfigurationName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIoTDomainConfiguration,
			NativeID:       arn,
			Name:           &dname,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
			// Names prefixed "iot:" identify the AWS-default domain
			// configurations (iot:Data-ATS, iot:Jobs, iot:CredentialProvider)
			// present in every account.
			ManagedByProvider: strings.HasPrefix(dname, "iot:"),
		}, nil
	}, st, "iot domain configurations")
}
