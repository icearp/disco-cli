package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pcaconnectorad"
	pcaadtypes "github.com/aws/aws-sdk-go-v2/service/pcaconnectorad/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePCAConnectorADConnector, Service: "pca-connector-ad"})
	registerType(restype.Descriptor{Type: TypePCAConnectorADDirectoryRegistration, Service: "pca-connector-ad"})
	registerType(restype.Descriptor{Type: TypePCAConnectorADServicePrincipalName, Service: "pca-connector-ad"})
	registerType(restype.Descriptor{Type: TypePCAConnectorADTemplate, Service: "pca-connector-ad"})
	registerType(restype.Descriptor{Type: TypePCAConnectorADTemplateGroupACE, Service: "pca-connector-ad"})
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
	ListTagsForResource(context.Context, *pcaconnectorad.ListTagsForResourceInput, ...func(*pcaconnectorad.Options)) (*pcaconnectorad.ListTagsForResourceOutput, error)
}

const (
	pcaADTagsOp = "pcaconnectorad:ListTagsForResource"
	// pcaADPageCap is the MaxResults every listing sends: the maximum of the
	// range the model gives all five (1-1000).
	pcaADPageCap = 1000
)

// pcaADGone is the not-found code ListServicePrincipalNames, ListTemplates,
// ListTemplateGroupAccessControlEntries and ListTagsForResource model: the
// parent of a child listing, or the resource whose tags are read, was deleted
// since it was listed.
var pcaADGone = []string{"ResourceNotFoundException"}

// scanPCAConnectorAD discovers Private CA AD connectors, directory
// registrations, per-directory-registration service principal names,
// per-connector templates, and per-template template-group ACEs. Every
// listing runs as its own phase, one at a time: a phase's error does not stop
// the later ones; a failed write and ctx's end do (runHostPhases). Each child
// phase reads the parents stored this scan, those parents concurrently.
// Connectors, directory registrations and templates have their tags read per
// row (pcaADTags), whose denials are one warning for the whole scan.
func scanPCAConnectorAD(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := pcaconnectorad.NewFromConfig(acct.cfg, func(o *pcaconnectorad.Options) { o.Region = region })
	tags := pcaADTags(client).sharingDenials()
	var connectors, dirRegs, templates []childParent
	total, err := runHostPhases(ctx, st, acct, region, "pcaconnectorad", 0, []hostPhase{
		{op: "pcaconnectorad:ListConnectors", run: func(p *phaseTotals) (failed bool) {
			connectors, failed = scanPCAConnectors(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "pcaconnectorad:ListDirectoryRegistrations", run: func(p *phaseTotals) (failed bool) {
			dirRegs, failed = scanPCADirectoryRegistrations(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "pcaconnectorad:ListServicePrincipalNames", run: func(p *phaseTotals) bool {
			return scanPCAServicePrincipalNames(ctx, client, acct, region, st, scanID, dirRegs, p)
		}},
		{op: "pcaconnectorad:ListTemplates", run: func(p *phaseTotals) (failed bool) {
			templates, failed = scanPCATemplates(ctx, client, acct, region, st, scanID, connectors, tags, p)
			return failed
		}},
		{op: "pcaconnectorad:ListTemplateGroupAccessControlEntries", run: func(p *phaseTotals) bool {
			return scanPCATemplateGroupACEs(ctx, client, acct, region, st, scanID, templates, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// pcaADTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference gives the
// connector, directory registration and template aws:ResourceTag and lists
// them under TagResource, and each one's Create* input carries tags; service
// principal names and template group ACEs have neither, so theirs are not
// read. The op does not page its tags. The service models no sharing (no RAM
// resource type, no cross-account listing), so every row is this account's
// own and no other-account gate applies.
func pcaADTags(client pcaADAPI) rowTagReader {
	return rowTagReader{
		op:       pcaADTagsOp,
		notFound: pcaADGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &pcaconnectorad.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// pcaADNamedByARN is the row of a connector or directory registration: stored
// under the ARN its element carries, named by that ARN, with the element's
// status and the element verbatim as the attributes. An element without an
// ARN is not stored (arnRow); its directory id names it in the warning.
func pcaADNamedByARN(acct *account, region, scanID, typ, what string, arn, directoryID *string, status string, item any) (*store.Resource, string, string) {
	return arnRow(what, arn, directoryID, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: arn, Region: &region, Status: &status,
		AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	})
}

// scanPCAConnectors stores the connectors with their tags, a page at a time,
// and returns the stored ones, the parents of the template phase, each keyed
// by its ARN. It reports whether a write failed, after which the scan must
// stop.
func scanPCAConnectors(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "pcaconnectorad:ListConnectors", "connectors", "Arn",
		func(ctx context.Context, token *string) ([]pcaadtypes.ConnectorSummary, *string, error) {
			out, err := client.ListConnectors(ctx, &pcaconnectorad.ListConnectorsInput{MaxResults: sdkaws.Int32(pcaADPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Connectors, out.NextToken, nil
		},
		func(c pcaadtypes.ConnectorSummary) (*store.Resource, string, string) {
			return pcaADNamedByARN(acct, region, scanID, TypePCAConnectorADConnector, "connectors", c.Arn, c.DirectoryId, string(c.Status), c)
		},
		func(c pcaadtypes.ConnectorSummary) string { return sv(c.Arn) },
		tags, p)
}

// scanPCADirectoryRegistrations is scanPCAConnectors for the directory
// registrations, the parents of the service principal name phase.
func scanPCADirectoryRegistrations(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "pcaconnectorad:ListDirectoryRegistrations", "directory registrations", "Arn",
		func(ctx context.Context, token *string) ([]pcaadtypes.DirectoryRegistrationSummary, *string, error) {
			out, err := client.ListDirectoryRegistrations(ctx, &pcaconnectorad.ListDirectoryRegistrationsInput{MaxResults: sdkaws.Int32(pcaADPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DirectoryRegistrations, out.NextToken, nil
		},
		func(d pcaadtypes.DirectoryRegistrationSummary) (*store.Resource, string, string) {
			return pcaADNamedByARN(acct, region, scanID, TypePCAConnectorADDirectoryRegistration, "directory registrations", d.Arn, d.DirectoryId, string(d.Status), d)
		},
		func(d pcaadtypes.DirectoryRegistrationSummary) string { return sv(d.Arn) },
		tags, p)
}

// scanPCAServicePrincipalNames stores the service principal names of each
// directory registration stored this scan, each page written as it is read;
// one registration's failure costs the others none of theirs. A service
// principal name has no ARN of its own: its NativeID is its directory
// registration's ARN + "/service-principal-name/" + its connector's full
// ARN, and an element without a connector ARN is not stored, with one
// warning. It reports whether a write failed, after which the scan must stop.
func scanPCAServicePrincipalNames(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string, dirRegs []childParent, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, "pcaconnectorad:ListServicePrincipalNames", pcaADGone, dirRegs,
		func(ctx context.Context, dirARN string, token *string) ([]pcaadtypes.ServicePrincipalNameSummary, *string, error) {
			out, err := client.ListServicePrincipalNames(ctx, &pcaconnectorad.ListServicePrincipalNamesInput{DirectoryRegistrationArn: &dirARN, MaxResults: sdkaws.Int32(pcaADPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ServicePrincipalNames, out.NextToken, nil
		},
		func(dirARN string, s pcaadtypes.ServicePrincipalNameSummary) (*store.Resource, string, string) {
			ca := sv(s.ConnectorArn)
			if ca == "" {
				return nil, "service principal names have no connector ARN", dirARN
			}
			status := string(s.Status)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCAConnectorADServicePrincipalName, NativeID: dirARN + "/service-principal-name/" + ca,
				Name: &ca, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}, "", ""
		},
		p)
}

// scanPCATemplates stores each connector's templates with their tags, each
// page written as soon as its tags are read; one connector's failure costs
// the others none of theirs. It returns the stored templates, the parents of
// the template group ACE phase, each keyed by its ARN. It reports whether a
// write failed, after which the scan must stop.
func scanPCATemplates(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string, connectors []childParent, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeNestedParentPagesTags(ctx, st, acct, region, "pcaconnectorad:ListTemplates", "templates", "Arn", pcaADGone, connectors,
		func(ctx context.Context, connectorARN string, token *string) ([]pcaadtypes.TemplateSummary, *string, error) {
			out, err := client.ListTemplates(ctx, &pcaconnectorad.ListTemplatesInput{ConnectorArn: &connectorARN, MaxResults: sdkaws.Int32(pcaADPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Templates, out.NextToken, nil
		},
		func(_ string, tt pcaadtypes.TemplateSummary) (*store.Resource, string, string) {
			return arnRow("templates", tt.Arn, tt.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCAConnectorADTemplate, NativeID: sv(tt.Arn),
				Name: tt.Name, Region: &region,
				AttributesJSON: mustJSON(tt), DiscoveredBy: scanID,
			})
		},
		func(tt pcaadtypes.TemplateSummary) string { return sv(tt.Arn) },
		tags, p)
}

// scanPCATemplateGroupACEs stores the group access control entries of each
// template stored this scan, each page written as it is read; one template's
// failure costs the others none of theirs. An entry has no ARN of its own:
// its NativeID is its template's ARN + "/access-control-entry/" + its group
// SID, and an entry without a SID is not stored, with one warning. It reports
// whether a write failed, after which the scan must stop.
func scanPCATemplateGroupACEs(ctx context.Context, client pcaADAPI, acct *account, region string, st *store.Store, scanID string, templates []childParent, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, "pcaconnectorad:ListTemplateGroupAccessControlEntries", pcaADGone, templates,
		func(ctx context.Context, templateARN string, token *string) ([]pcaadtypes.AccessControlEntrySummary, *string, error) {
			out, err := client.ListTemplateGroupAccessControlEntries(ctx, &pcaconnectorad.ListTemplateGroupAccessControlEntriesInput{TemplateArn: &templateARN, MaxResults: sdkaws.Int32(pcaADPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessControlEntries, out.NextToken, nil
		},
		func(templateARN string, e pcaadtypes.AccessControlEntrySummary) (*store.Resource, string, string) {
			sid := sv(e.GroupSecurityIdentifier)
			if sid == "" {
				return nil, "access control entries have no group SID", cmp.Or(sv(e.GroupDisplayName), templateARN)
			}
			label := cmp.Or(sv(e.GroupDisplayName), sid)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCAConnectorADTemplateGroupACE, NativeID: templateARN + "/access-control-entry/" + sid,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			}, "", ""
		},
		p)
}
