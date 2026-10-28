package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/proton"
	protontypes "github.com/aws/aws-sdk-go-v2/service/proton/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeProtonComponent, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonDeployment, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonEnvironment, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonEnvironmentAccountConnection, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonEnvironmentTemplate, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonEnvironmentTemplateVersion, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonRepository, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonService, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonServiceInstance, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonServiceTemplate, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonServiceTemplateVersion, Service: "proton"})
	registerService(serviceEntry{
		name: "aws:proton",
		fn:   scanProton,
	})
}

type protonAPI interface {
	ListEnvironmentAccountConnections(context.Context, *proton.ListEnvironmentAccountConnectionsInput, ...func(*proton.Options)) (*proton.ListEnvironmentAccountConnectionsOutput, error)
	ListEnvironmentTemplates(context.Context, *proton.ListEnvironmentTemplatesInput, ...func(*proton.Options)) (*proton.ListEnvironmentTemplatesOutput, error)
	ListServiceTemplates(context.Context, *proton.ListServiceTemplatesInput, ...func(*proton.Options)) (*proton.ListServiceTemplatesOutput, error)
	ListComponents(context.Context, *proton.ListComponentsInput, ...func(*proton.Options)) (*proton.ListComponentsOutput, error)
	ListDeployments(context.Context, *proton.ListDeploymentsInput, ...func(*proton.Options)) (*proton.ListDeploymentsOutput, error)
	ListEnvironments(context.Context, *proton.ListEnvironmentsInput, ...func(*proton.Options)) (*proton.ListEnvironmentsOutput, error)
	ListServices(context.Context, *proton.ListServicesInput, ...func(*proton.Options)) (*proton.ListServicesOutput, error)
	ListServiceInstances(context.Context, *proton.ListServiceInstancesInput, ...func(*proton.Options)) (*proton.ListServiceInstancesOutput, error)
	ListRepositories(context.Context, *proton.ListRepositoriesInput, ...func(*proton.Options)) (*proton.ListRepositoriesOutput, error)
	ListEnvironmentTemplateVersions(context.Context, *proton.ListEnvironmentTemplateVersionsInput, ...func(*proton.Options)) (*proton.ListEnvironmentTemplateVersionsOutput, error)
	ListServiceTemplateVersions(context.Context, *proton.ListServiceTemplateVersionsInput, ...func(*proton.Options)) (*proton.ListServiceTemplateVersionsOutput, error)
	ListTagsForResource(context.Context, *proton.ListTagsForResourceInput, ...func(*proton.Options)) (*proton.ListTagsForResourceOutput, error)
}

// protonPageCap is the maxResults maximum of the MaxPageResults range trait
// (1-100) that every Proton list op this scanner calls, the tag op included,
// takes as its page size.
const protonPageCap int32 = 100

// protonTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the ARN its listing returned, and reads every page. The
// Service Reference lists every tagged type's ARN format, the versioned
// template-version formats included, as a resource of the op.
func protonTags(client protonAPI) rowTagReader {
	return rowTagReader{
		op:       "proton:ListTagsForResource",
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]protontypes.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &proton.ListTagsForResourceInput{ResourceArn: &arn, NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t protontypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// protonConnectionTags is protonTags for environment account connections. A
// connection is created in its environment account
// (CreateEnvironmentAccountConnection's doc) and is listed from its management
// account too (ListEnvironmentAccountConnections RequestedBy
// MANAGEMENT_ACCOUNT), so a listed connection's ARN can carry another
// account; such a connection is read under otherAccountGap.
func protonConnectionTags(tags rowTagReader, acct *account) rowTagReader {
	return tags.untaggedIfOtherAccount(acct.ID)
}

// scanProton discovers Proton account connections, templates, template
// versions, environments, services, service instances, components,
// deployments and repositories. Every listing runs as its own phase: a
// phase's error does not stop the later ones; a failed write and ctx's end
// do (runHostPhases). The phases run one at a time, so readEachKey's own
// bound is the bound on the reads in flight. Template versions are listed per
// template stored this scan, since their op needs a TemplateName.
//
// Deployments are stored without tags: ListTagsForResource and TagResource
// do not list the deployment resource in the Service Reference, and no op
// creates a deployment with tags.
func scanProton(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := proton.NewFromConfig(acct.cfg, func(o *proton.Options) { o.Region = region })
	tags := protonTags(client)
	var envTemplates, svcTemplates []childParent
	return runHostPhases(ctx, st, acct, region, "proton", 0, []hostPhase{
		{op: "proton:ListEnvironmentAccountConnections", run: func(p *phaseTotals) bool {
			return scanProtonAccountConnections(ctx, client, acct, region, st, scanID, protonConnectionTags(tags, acct), p)
		}},
		{op: "proton:ListEnvironmentTemplates", run: func(p *phaseTotals) (failed bool) {
			envTemplates, failed = scanProtonEnvTemplates(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "proton:ListServiceTemplates", run: func(p *phaseTotals) (failed bool) {
			svcTemplates, failed = scanProtonServiceTemplates(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "proton:ListEnvironments", run: func(p *phaseTotals) bool {
			return scanProtonEnvironments(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "proton:ListServices", run: func(p *phaseTotals) bool {
			return scanProtonServices(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "proton:ListServiceInstances", run: func(p *phaseTotals) bool {
			return scanProtonServiceInstances(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "proton:ListComponents", run: func(p *phaseTotals) bool {
			return scanProtonComponents(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "proton:ListDeployments", func() (int, int, error) {
			return scanProtonDeployments(ctx, client, acct, region, st, scanID)
		}),
		{op: "proton:ListRepositories", run: func(p *phaseTotals) bool {
			return scanProtonRepositories(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "proton:ListEnvironmentTemplateVersions", run: func(p *phaseTotals) bool {
			return scanProtonEnvTemplateVersions(ctx, client, acct, region, st, scanID, envTemplates, tags, p)
		}},
		{op: "proton:ListServiceTemplateVersions", run: func(p *phaseTotals) bool {
			return scanProtonServiceTemplateVersions(ctx, client, acct, region, st, scanID, svcTemplates, tags, p)
		}},
	})
}

// protonRow is the row of a listed element whose NativeID is its ARN, named
// by name, or by its ARN when name is empty. An element without an ARN is not
// stored; what names the elements in the warning and key the element.
func protonRow(acct *account, region, scanID, typ, what, arn, name, status, key string, item any) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(key, "(an element with no identifier)")
	}
	label := cmp.Or(name, arn)
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &label, Region: &region, Status: nonEmptyPtr(status),
		AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// scanProtonAccountConnections lists the connections requested by either
// side, since the op takes one RequestedBy value per call, and stores each
// once: a connection is expected under one value only, so a repeat is
// dropped by ARN rather than written twice. One side's failure keeps the
// other side's rows and the pages read before it.
func scanProtonAccountConnections(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	const op = "proton:ListEnvironmentAccountConnections"
	var (
		items  []protontypes.EnvironmentAccountConnectionSummary
		errs   []error
		denied error
	)
	for _, rb := range []protontypes.EnvironmentAccountConnectionRequesterAccountType{
		protontypes.EnvironmentAccountConnectionRequesterAccountTypeManagementAccount,
		protontypes.EnvironmentAccountConnectionRequesterAccountTypeEnvironmentAccount,
	} {
		got, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]protontypes.EnvironmentAccountConnectionSummary, *string, error) {
			out, err := client.ListEnvironmentAccountConnections(ctx, &proton.ListEnvironmentAccountConnectionsInput{RequestedBy: rb, NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.EnvironmentAccountConnections, out.NextToken, nil
		})
		items = append(items, got...)
		switch {
		case err == nil:
		case isAccessDenied(err):
			denied = cmp.Or(denied, fmt.Errorf("RequestedBy %s: %w", rb, err))
		default:
			errs = append(errs, fmt.Errorf("RequestedBy %s: %w", rb, err))
		}
	}
	seen := map[string]bool{}
	var skips notStoredSkips
	rows := buildListedRows(items, func(c protontypes.EnvironmentAccountConnectionSummary) (*store.Resource, string, string) {
		return protonRow(acct, region, scanID, TypeProtonEnvironmentAccountConnection, "environment account connections",
			sv(c.Arn), cmp.Or(sv(c.EnvironmentName), sv(c.Id)), string(c.Status), sv(c.Id), c)
	}, nil, &skips)
	unique := rows[:0]
	for _, r := range rows {
		if !seen[r.NativeID] {
			seen[r.NativeID] = true
			unique = append(unique, r)
		}
	}
	return storeRowsTags(ctx, st, acct, region, op, unique, errors.Join(errs...), func() {
		skips.warn(st, op, acct, region)
		if denied != nil {
			_ = skipIfAccessDenied(st, op, acct.ID, region, denied)
		}
	}, tags, p)
}

// scanProtonEnvTemplates stores the environment templates with their tags and
// returns the stored ones, keyed by Name, the parents of the version phase.
func scanProtonEnvTemplates(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "proton:ListEnvironmentTemplates", "environment templates", "Name",
		func(ctx context.Context, token *string) ([]protontypes.EnvironmentTemplateSummary, *string, error) {
			out, err := client.ListEnvironmentTemplates(ctx, &proton.ListEnvironmentTemplatesInput{NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Templates, out.NextToken, nil
		},
		func(t protontypes.EnvironmentTemplateSummary) (*store.Resource, string, string) {
			return protonRow(acct, region, scanID, TypeProtonEnvironmentTemplate, "environment templates", sv(t.Arn), sv(t.Name), "", sv(t.Name), t)
		},
		func(t protontypes.EnvironmentTemplateSummary) string { return sv(t.Name) },
		tags, p)
}

// scanProtonServiceTemplates stores the service templates with their tags and
// returns the stored ones, keyed by Name, the parents of the version phase.
func scanProtonServiceTemplates(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "proton:ListServiceTemplates", "service templates", "Name",
		func(ctx context.Context, token *string) ([]protontypes.ServiceTemplateSummary, *string, error) {
			out, err := client.ListServiceTemplates(ctx, &proton.ListServiceTemplatesInput{NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Templates, out.NextToken, nil
		},
		func(t protontypes.ServiceTemplateSummary) (*store.Resource, string, string) {
			return protonRow(acct, region, scanID, TypeProtonServiceTemplate, "service templates", sv(t.Arn), sv(t.Name), "", sv(t.Name), t)
		},
		func(t protontypes.ServiceTemplateSummary) string { return sv(t.Name) },
		tags, p)
}

func scanProtonEnvironments(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "proton:ListEnvironments",
		func(ctx context.Context, token *string) ([]protontypes.EnvironmentSummary, *string, error) {
			out, err := client.ListEnvironments(ctx, &proton.ListEnvironmentsInput{NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Environments, out.NextToken, nil
		},
		func(e protontypes.EnvironmentSummary) (*store.Resource, string, string) {
			return protonRow(acct, region, scanID, TypeProtonEnvironment, "environments", sv(e.Arn), sv(e.Name), string(e.DeploymentStatus), sv(e.Name), e)
		}, tags, p)
}

func scanProtonServices(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "proton:ListServices",
		func(ctx context.Context, token *string) ([]protontypes.ServiceSummary, *string, error) {
			out, err := client.ListServices(ctx, &proton.ListServicesInput{NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Services, out.NextToken, nil
		},
		func(s protontypes.ServiceSummary) (*store.Resource, string, string) {
			return protonRow(acct, region, scanID, TypeProtonService, "services", sv(s.Arn), sv(s.Name), string(s.Status), sv(s.Name), s)
		}, tags, p)
}

func scanProtonServiceInstances(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "proton:ListServiceInstances",
		func(ctx context.Context, token *string) ([]protontypes.ServiceInstanceSummary, *string, error) {
			out, err := client.ListServiceInstances(ctx, &proton.ListServiceInstancesInput{NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ServiceInstances, out.NextToken, nil
		},
		func(si protontypes.ServiceInstanceSummary) (*store.Resource, string, string) {
			return protonRow(acct, region, scanID, TypeProtonServiceInstance, "service instances", sv(si.Arn), sv(si.Name), string(si.DeploymentStatus), sv(si.ServiceName)+"/"+sv(si.Name), si)
		}, tags, p)
}

func scanProtonComponents(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "proton:ListComponents",
		func(ctx context.Context, token *string) ([]protontypes.ComponentSummary, *string, error) {
			out, err := client.ListComponents(ctx, &proton.ListComponentsInput{NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Components, out.NextToken, nil
		},
		func(c protontypes.ComponentSummary) (*store.Resource, string, string) {
			return protonRow(acct, region, scanID, TypeProtonComponent, "components", sv(c.Arn), sv(c.Name), string(c.DeploymentStatus), sv(c.Name), c)
		}, tags, p)
}

// scanProtonDeployments stores the deployments, without tags (scanProton
// says why).
func scanProtonDeployments(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "proton:ListDeployments",
		func(ctx context.Context, token *string) ([]protontypes.DeploymentSummary, *string, error) {
			out, err := client.ListDeployments(ctx, &proton.ListDeploymentsInput{NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Deployments, out.NextToken, nil
		},
		func(d protontypes.DeploymentSummary) (*store.Resource, string, string) {
			return protonRow(acct, region, scanID, TypeProtonDeployment, "deployments", sv(d.Arn), sv(d.Id), string(d.DeploymentStatus), sv(d.Id), d)
		})
}

func scanProtonRepositories(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "proton:ListRepositories",
		func(ctx context.Context, token *string) ([]protontypes.RepositorySummary, *string, error) {
			out, err := client.ListRepositories(ctx, &proton.ListRepositoriesInput{NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Repositories, out.NextToken, nil
		},
		func(r protontypes.RepositorySummary) (*store.Resource, string, string) {
			return protonRow(acct, region, scanID, TypeProtonRepository, "repositories", sv(r.Arn), sv(r.Name), "", string(r.Provider)+":"+sv(r.Name), r)
		}, tags, p)
}

// protonVersionName is a template version's Name, "{major}.{minor}".
func protonVersionName(major, minor *string) string { return sv(major) + "." + sv(minor) }

// scanProtonEnvTemplateVersions lists the versions of each environment
// template the template phase stored and stores them with their tags
// (storeEachParentTags). A template ResourceNotFoundException says was
// deleted since it was listed is skipped.
func scanProtonEnvTemplateVersions(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, templates []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, failed := storeEachParentTags(ctx, st, acct, region, "proton:ListEnvironmentTemplateVersions", []string{"ResourceNotFoundException"}, templates,
		func(ctx context.Context, template string, token *string) ([]protontypes.EnvironmentTemplateVersionSummary, *string, error) {
			out, err := client.ListEnvironmentTemplateVersions(ctx, &proton.ListEnvironmentTemplateVersionsInput{TemplateName: &template, NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TemplateVersions, out.NextToken, nil
		},
		func(template string, v protontypes.EnvironmentTemplateVersionSummary) (*store.Resource, string, string) {
			name := protonVersionName(v.MajorVersion, v.MinorVersion)
			return protonRow(acct, region, scanID, TypeProtonEnvironmentTemplateVersion, "environment template versions", sv(v.Arn), name, string(v.Status), template+":"+name, v)
		}, tags, p)
	return failed
}

// scanProtonServiceTemplateVersions is scanProtonEnvTemplateVersions for
// service templates.
func scanProtonServiceTemplateVersions(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, templates []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, failed := storeEachParentTags(ctx, st, acct, region, "proton:ListServiceTemplateVersions", []string{"ResourceNotFoundException"}, templates,
		func(ctx context.Context, template string, token *string) ([]protontypes.ServiceTemplateVersionSummary, *string, error) {
			out, err := client.ListServiceTemplateVersions(ctx, &proton.ListServiceTemplateVersionsInput{TemplateName: &template, NextToken: token, MaxResults: sdkaws.Int32(protonPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TemplateVersions, out.NextToken, nil
		},
		func(template string, v protontypes.ServiceTemplateVersionSummary) (*store.Resource, string, string) {
			name := protonVersionName(v.MajorVersion, v.MinorVersion)
			return protonRow(acct, region, scanID, TypeProtonServiceTemplateVersion, "service template versions", sv(v.Arn), name, string(v.Status), template+":"+name, v)
		}, tags, p)
	return failed
}
