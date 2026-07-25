package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/pcaconnectorad"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePCAConnectorADConnector, Service: "pca-connector-ad"})
	registerType(restype.Descriptor{Type: TypePCAConnectorADDirectoryRegistration, Service: "pca-connector-ad"})
	registerType(restype.Descriptor{Type: TypePCAConnectorADServicePrincipalName, Service: "pca-connector-ad", Leaf: true})
	registerType(restype.Descriptor{Type: TypePCAConnectorADTemplate, Service: "pca-connector-ad", Leaf: true})
	registerType(restype.Descriptor{Type: TypePCAConnectorADTemplateGroupACE, Service: "pca-connector-ad", Leaf: true})
	registerService(serviceEntry{
		name: "aws:pca-connector-ad",
		fn:   scanPCAConnectorAD,
	})
}

type pcaADAPI interface {
	ListConnectors(context.Context, *pcaconnectorad.ListConnectorsInput, ...func(*pcaconnectorad.Options)) (*pcaconnectorad.ListConnectorsOutput, error)
	ListDirectoryRegistrations(context.Context, *pcaconnectorad.ListDirectoryRegistrationsInput, ...func(*pcaconnectorad.Options)) (*pcaconnectorad.ListDirectoryRegistrationsOutput, error)
	ListServicePrincipalNames(context.Context, *pcaconnectorad.ListServicePrincipalNamesInput, ...func(*pcaconnectorad.Options)) (*pcaconnectorad.ListServicePrincipalNamesOutput, error)
	ListTemplates(context.Context, *pcaconnectorad.ListTemplatesInput, ...func(*pcaconnectorad.Options)) (*pcaconnectorad.ListTemplatesOutput, error)
	ListTemplateGroupAccessControlEntries(context.Context, *pcaconnectorad.ListTemplateGroupAccessControlEntriesInput, ...func(*pcaconnectorad.Options)) (*pcaconnectorad.ListTemplateGroupAccessControlEntriesOutput, error)
}

// scanPCAConnectorAD discovers Private CA AD connectors, directory
// registrations, per-directory-registration service principal names,
// per-connector templates, and per-template template-group ACEs.
func scanPCAConnectorAD(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := pcaconnectorad.NewFromConfig(acct.cfg, func(o *pcaconnectorad.Options) { o.Region = region })

	connectorARNs, t, i, ferr := scanPCAConnectors(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	dirARNs, t, i, ferr := scanPCADirectoryRegistrations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, da := range dirARNs {
		t, i, ferr = scanPCAServicePrincipalNames(ctx, client, acct, region, st, scanID, da)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	var templateARNs []string
	for _, ca := range connectorARNs {
		ts, tt, ii, terr := scanPCATemplates(ctx, client, acct, region, st, scanID, ca)
		if terr != nil {
			return total, inserted, terr
		}
		total += tt
		inserted += ii
		templateARNs = append(templateARNs, ts...)
	}
	for _, ta := range templateARNs {
		t, i, ferr = scanPCATemplateGroupACEs(ctx, client, acct, region, st, scanID, ta)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanPCAConnectors(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := pcaconnectorad.NewListConnectorsPaginator(client, &pcaconnectorad.ListConnectorsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "pcaconnectorad:ListConnectors", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("pcaconnectorad:ListConnectors: %w", err)
		}
		for _, c := range out.Connectors {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := arn
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCAConnectorADConnector, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "pcaconnectorad connectors")
	return arns, t, i, err
}

func scanPCADirectoryRegistrations(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := pcaconnectorad.NewListDirectoryRegistrationsPaginator(client, &pcaconnectorad.ListDirectoryRegistrationsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "pcaconnectorad:ListDirectoryRegistrations", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("pcaconnectorad:ListDirectoryRegistrations: %w", err)
		}
		for _, d := range out.DirectoryRegistrations {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := arn
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCAConnectorADDirectoryRegistration, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "pcaconnectorad directory-registrations")
	return arns, t, i, err
}

// scanPCAServicePrincipalNames synthesizes ARN: parent directory-reg ARN +
// /service-principal-name/{connectorArnTail}. SPNs have no native ARN.
func scanPCAServicePrincipalNames(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string, dirARN string) (int, int, error) {
	da := dirARN
	pager := pcaconnectorad.NewListServicePrincipalNamesPaginator(client, &pcaconnectorad.ListServicePrincipalNamesInput{DirectoryRegistrationArn: &da})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "pcaconnectorad:ListServicePrincipalNames", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("pcaconnectorad:ListServicePrincipalNames: %w", err)
		}
		for _, s := range out.ServicePrincipalNames {
			ca := sv(s.ConnectorArn)
			if ca == "" {
				continue
			}
			arn := da + "/service-principal-name/" + ca
			label := ca
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCAConnectorADServicePrincipalName, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "pcaconnectorad service-principal-names")
}

func scanPCATemplates(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string, connectorARN string) ([]string, int, int, error) {
	ca := connectorARN
	pager := pcaconnectorad.NewListTemplatesPaginator(client, &pcaconnectorad.ListTemplatesInput{ConnectorArn: &ca})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "pcaconnectorad:ListTemplates", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("pcaconnectorad:ListTemplates: %w", err)
		}
		for _, tt := range out.Templates {
			arn := sv(tt.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCAConnectorADTemplate, NativeID: arn,
				Name: tt.Name, Region: &region,
				AttributesJSON: mustJSON(tt), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "pcaconnectorad templates")
	return arns, t, i, err
}

// scanPCATemplateGroupACEs synthesizes ARN: parent template ARN +
// /access-control-entry/{groupSID}.
func scanPCATemplateGroupACEs(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string, templateARN string) (int, int, error) {
	ta := templateARN
	pager := pcaconnectorad.NewListTemplateGroupAccessControlEntriesPaginator(client, &pcaconnectorad.ListTemplateGroupAccessControlEntriesInput{TemplateArn: &ta})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "pcaconnectorad:ListTemplateGroupAccessControlEntries", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("pcaconnectorad:ListTemplateGroupAccessControlEntries: %w", err)
		}
		for _, e := range out.AccessControlEntries {
			sid := sv(e.GroupSecurityIdentifier)
			if sid == "" {
				continue
			}
			arn := ta + "/access-control-entry/" + sid
			label := sv(e.GroupDisplayName)
			if label == "" {
				label = sid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCAConnectorADTemplateGroupACE, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "pcaconnectorad template-group-aces")
}
