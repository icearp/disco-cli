package aws

import (
	"context"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTCertificate, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTCACertificate, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTCertificateProvider, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTPolicy, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTPolicyPrincipalAttachment, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTRoleAlias, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTAuthorizer, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTDomainConfiguration, Service: "iot"})
}

// iotCertsAPI is the narrow surface used by the Certs/Auth family.
type iotCertsAPI interface {
	iotTagsAPI
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

// scanIoTCerts runs Certs/Auth-family phases, adding each to p, and reports
// whether a write failed.
func scanIoTCerts(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	// Listed once for the policy phase and the principal phase after it.
	var policies *iotListing
	return runIoTPhases(p,
		func(p *phaseTotals) bool { return scanIoTCertificates(ctx, client, acct, region, st, scanID, p) },
		func(p *phaseTotals) bool {
			return scanIoTCACertificates(ctx, client, acct, region, st, scanID, p, tags)
		},
		func(p *phaseTotals) bool {
			return scanIoTCertificateProviders(ctx, client, acct, region, st, scanID, p, tags)
		},
		func(p *phaseTotals) bool {
			policies = iotListPolicies(ctx, client, acct, region, scanID)
			return scanIoTPolicies(ctx, client, acct, region, st, scanID, p, tags, policies)
		},
		func(p *phaseTotals) bool {
			return scanIoTPolicyPrincipalAttachments(ctx, client, acct, region, st, scanID, p, policies.keys)
		},
		func(p *phaseTotals) bool { return scanIoTRoleAliases(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTAuthorizers(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool {
			return scanIoTDomainConfigurations(ctx, client, acct, region, st, scanID, p, tags)
		},
	)
}

func scanIoTCertificates(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListCertificates", "iot certificates", "CertificateId",
		func(ctx context.Context, marker *string) ([]iottypes.Certificate, *string, error) {
			out, err := client.ListCertificates(ctx, &iot.ListCertificatesInput{Marker: marker, PageSize: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Certificates, out.NextMarker, nil
		},
		func(c iottypes.Certificate) *string { return c.CertificateId },
		func(c iottypes.Certificate) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTCertificate, c.CertificateArn, c.CertificateId, c)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeCertificate", isIoTNotFound,
		func(ctx context.Context, id string) (*store.Resource, error) {
			out, err := client.DescribeCertificate(ctx, &iot.DescribeCertificateInput{CertificateId: &id})
			if err != nil {
				return nil, err
			}
			if out.CertificateDescription == nil || sv(out.CertificateDescription.CertificateArn) == "" {
				return nil, nil
			}
			cid := sv(out.CertificateDescription.CertificateId)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTCertificate,
				NativeID:       *out.CertificateDescription.CertificateArn,
				Name:           &cid,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStore(st, acct, region, p, list, d)
}

func scanIoTCACertificates(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListCACertificates", "iot ca certificates", "CertificateId",
		func(ctx context.Context, marker *string) ([]iottypes.CACertificate, *string, error) {
			out, err := client.ListCACertificates(ctx, &iot.ListCACertificatesInput{Marker: marker, PageSize: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Certificates, out.NextMarker, nil
		},
		func(c iottypes.CACertificate) *string { return c.CertificateId },
		func(c iottypes.CACertificate) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTCACertificate, c.CertificateArn, c.CertificateId, c)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeCACertificate", isIoTNotFound,
		func(ctx context.Context, id string) (*store.Resource, error) {
			out, err := client.DescribeCACertificate(ctx, &iot.DescribeCACertificateInput{CertificateId: &id})
			if err != nil {
				return nil, err
			}
			if out.CertificateDescription == nil || sv(out.CertificateDescription.CertificateArn) == "" {
				return nil, nil
			}
			cid := sv(out.CertificateDescription.CertificateId)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTCACertificate,
				NativeID:       *out.CertificateDescription.CertificateArn,
				Name:           &cid,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

// scanIoTCertificateProviders — the op has no page-size input.
func scanIoTCertificateProviders(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListCertificateProviders", "iot certificate providers", "CertificateProviderName",
		func(ctx context.Context, token *string) ([]iottypes.CertificateProviderSummary, *string, error) {
			out, err := client.ListCertificateProviders(ctx, &iot.ListCertificateProvidersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CertificateProviders, out.NextToken, nil
		},
		func(c iottypes.CertificateProviderSummary) *string { return c.CertificateProviderName },
		func(c iottypes.CertificateProviderSummary) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTCertificateProvider, c.CertificateProviderArn, c.CertificateProviderName, c)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeCertificateProvider", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, err := client.DescribeCertificateProvider(ctx, &iot.DescribeCertificateProviderInput{CertificateProviderName: &name})
			if err != nil {
				return nil, err
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
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

// iotDefaultDomainConfiguration reports whether name is one of the
// AWS-default domain configurations (iot:Data-ATS, iot:Jobs,
// iot:CredentialProvider) present in every account: their names, and only
// theirs, are prefixed "iot:".
func iotDefaultDomainConfiguration(name string) bool { return strings.HasPrefix(name, "iot:") }

// iotListPolicies lists the policies at the model's page-size cap.
func iotListPolicies(ctx context.Context, client iotCertsAPI, acct *account, region, scanID string) *iotListing {
	return iotList(ctx, "iot:ListPolicies", "iot policies", "PolicyName",
		func(ctx context.Context, marker *string) ([]iottypes.Policy, *string, error) {
			out, err := client.ListPolicies(ctx, &iot.ListPoliciesInput{Marker: marker, PageSize: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Policies, out.NextMarker, nil
		},
		func(pol iottypes.Policy) *string { return pol.PolicyName },
		func(pol iottypes.Policy) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTPolicy, pol.PolicyArn, pol.PolicyName, pol)
		})
}

// scanIoTPolicies describes and stores the policies list holds.
func scanIoTPolicies(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags, list *iotListing) (storeWriteFailed bool) {
	d := list.describe(ctx, st, acct, region, "iot:GetPolicy", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, err := client.GetPolicy(ctx, &iot.GetPolicyInput{PolicyName: &name})
			if err != nil {
				return nil, err
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
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

// scanIoTPolicyPrincipalAttachments stores one row per (policy, principal)
// pair, listing each policy's principals (scanIoTPrincipals). NativeID is
// synthesized since the attachment has no AWS-issued ARN.
func scanIoTPolicyPrincipalAttachments(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, policies []string) (storeWriteFailed bool) {
	return scanIoTPrincipals(ctx, st, acct, region, scanID, p, iotPrincipalPhase{
		op: "iot:ListPolicyPrincipals", what: "iot policies' principals", kind: "policy", keyAttr: "PolicyName",
		typ: TypeIoTPolicyPrincipalAttachment,
	}, policies, func(policy string) func(context.Context, *string) ([]string, *string, error) {
		return func(ctx context.Context, marker *string) ([]string, *string, error) {
			out, err := client.ListPolicyPrincipals(ctx, &iot.ListPolicyPrincipalsInput{
				PolicyName: &policy, Marker: marker, PageSize: sdkaws.Int32(iotListPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Principals, out.NextMarker, nil
		}
	})
}

func scanIoTRoleAliases(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListRoleAliases", "iot role aliases", "RoleAlias",
		func(ctx context.Context, marker *string) ([]string, *string, error) {
			out, err := client.ListRoleAliases(ctx, &iot.ListRoleAliasesInput{Marker: marker, PageSize: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.RoleAliases, out.NextMarker, nil
		}, iotName, nil)
	d := list.describe(ctx, st, acct, region, "iot:DescribeRoleAlias", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, err := client.DescribeRoleAlias(ctx, &iot.DescribeRoleAliasInput{RoleAlias: &name})
			if err != nil {
				return nil, err
			}
			if out.RoleAliasDescription == nil || sv(out.RoleAliasDescription.RoleAliasArn) == "" {
				return nil, nil
			}
			alias := sv(out.RoleAliasDescription.RoleAlias)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTRoleAlias,
				NativeID:       *out.RoleAliasDescription.RoleAliasArn,
				Name:           &alias,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTAuthorizers(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListAuthorizers", "iot authorizers", "AuthorizerName",
		func(ctx context.Context, marker *string) ([]iottypes.AuthorizerSummary, *string, error) {
			out, err := client.ListAuthorizers(ctx, &iot.ListAuthorizersInput{Marker: marker, PageSize: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Authorizers, out.NextMarker, nil
		},
		func(a iottypes.AuthorizerSummary) *string { return a.AuthorizerName },
		func(a iottypes.AuthorizerSummary) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTAuthorizer, a.AuthorizerArn, a.AuthorizerName, a)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeAuthorizer", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, err := client.DescribeAuthorizer(ctx, &iot.DescribeAuthorizerInput{AuthorizerName: &name})
			if err != nil {
				return nil, err
			}
			if out.AuthorizerDescription == nil || sv(out.AuthorizerDescription.AuthorizerArn) == "" {
				return nil, nil
			}
			aname := sv(out.AuthorizerDescription.AuthorizerName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTAuthorizer,
				NativeID:       *out.AuthorizerDescription.AuthorizerArn,
				Name:           &aname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

// scanIoTDomainConfigurations does not tag-read the AWS-default domain
// configurations (iotUntaggedManaged).
func scanIoTDomainConfigurations(ctx context.Context, client iotCertsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListDomainConfigurations", "iot domain configurations", "DomainConfigurationName",
		func(ctx context.Context, marker *string) ([]iottypes.DomainConfigurationSummary, *string, error) {
			out, err := client.ListDomainConfigurations(ctx, &iot.ListDomainConfigurationsInput{Marker: marker, PageSize: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.DomainConfigurations, out.NextMarker, nil
		},
		func(dc iottypes.DomainConfigurationSummary) *string { return dc.DomainConfigurationName },
		func(dc iottypes.DomainConfigurationSummary) *store.Resource {
			r := iotListedRow(acct, region, scanID, TypeIoTDomainConfiguration, dc.DomainConfigurationArn, dc.DomainConfigurationName, dc)
			if r != nil {
				r.ManagedByProvider = iotDefaultDomainConfiguration(sv(dc.DomainConfigurationName))
			}
			return r
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeDomainConfiguration", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, err := client.DescribeDomainConfiguration(ctx, &iot.DescribeDomainConfigurationInput{DomainConfigurationName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.DomainConfigurationArn)
			if arn == "" {
				return nil, nil
			}
			dname := sv(out.DomainConfigurationName)
			return &store.Resource{
				Provider:          "aws",
				AccountID:         acct.ID,
				AccountName:       &acct.Name,
				Type:              TypeIoTDomainConfiguration,
				NativeID:          arn,
				Name:              &dname,
				Region:            &region,
				AttributesJSON:    mustJSON(out),
				DiscoveredBy:      scanID,
				ManagedByProvider: iotDefaultDomainConfiguration(dname),
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, iotUntaggedManaged(tags.reader()))
}
