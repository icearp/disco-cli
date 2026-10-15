package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/connectcases"
	connectcasestypes "github.com/aws/aws-sdk-go-v2/service/connectcases/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCasesDomain, Service: "cases"})
	registerType(restype.Descriptor{Type: TypeCasesCaseRule, Service: "cases"})
	registerType(restype.Descriptor{Type: TypeCasesField, Service: "cases"})
	registerType(restype.Descriptor{Type: TypeCasesFieldOption, Service: "cases"})
	registerType(restype.Descriptor{Type: TypeCasesLayout, Service: "cases"})
	registerType(restype.Descriptor{Type: TypeCasesTemplate, Service: "cases"})
	registerService(serviceEntry{
		name: "aws:cases",
		fn:   scanCases,
	})
}

type casesAPI interface {
	ListDomains(context.Context, *connectcases.ListDomainsInput, ...func(*connectcases.Options)) (*connectcases.ListDomainsOutput, error)
	ListCaseRules(context.Context, *connectcases.ListCaseRulesInput, ...func(*connectcases.Options)) (*connectcases.ListCaseRulesOutput, error)
	ListFields(context.Context, *connectcases.ListFieldsInput, ...func(*connectcases.Options)) (*connectcases.ListFieldsOutput, error)
	ListFieldOptions(context.Context, *connectcases.ListFieldOptionsInput, ...func(*connectcases.Options)) (*connectcases.ListFieldOptionsOutput, error)
	ListLayouts(context.Context, *connectcases.ListLayoutsInput, ...func(*connectcases.Options)) (*connectcases.ListLayoutsOutput, error)
	ListTemplates(context.Context, *connectcases.ListTemplatesInput, ...func(*connectcases.Options)) (*connectcases.ListTemplatesOutput, error)
}

// scanCases discovers Connect Cases domains and per-domain case rules,
// fields, layouts, and templates, then the options of the single-select
// fields.
func scanCases(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := connectcases.NewFromConfig(acct.cfg, func(o *connectcases.Options) { o.Region = region })
	return scanCasesAll(ctx, client, acct, region, st, scanID)
}

// scanCasesAll runs the older phases, which stop at their first hard error,
// then the field-option phase over the single-select fields stored before that
// error, so a failure on either side costs the other no rows. The older side's
// transient error is demoted to a warning: one transient member would make the
// dispatcher read the whole join as transient, hiding the option phase's hard
// error and discarding every phase's counts.
func scanCasesAll(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, selectFields, err := scanCasesOlderPhases(ctx, client, acct, region, st, scanID)
	t, i, oerr := scanCasesFieldOptions(ctx, client, acct, region, st, scanID, selectFields)
	return total + t, inserted + i, errors.Join(demoteTransient(st, "cases", acct, region, err), oerr)
}

// scanCasesOlderPhases runs the phases that predate field options, stopping
// at the first hard error, and returns the single-select fields stored before
// it.
func scanCasesOlderPhases(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, selectFields []casesSelectField, err error) {
	domainIDs, t, i, ferr := scanCasesDomains(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, nil, ferr
	}
	total += t
	inserted += i

	for _, d := range domainIDs {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanCasesCaseRules(ctx, client, acct, region, st, scanID, d) },
			func() (int, int, error) {
				fields, t, i, err := scanCasesFields(ctx, client, acct, region, st, scanID, d)
				selectFields = append(selectFields, fields...)
				return t, i, err
			},
			func() (int, int, error) { return scanCasesLayouts(ctx, client, acct, region, st, scanID, d) },
			func() (int, int, error) { return scanCasesTemplates(ctx, client, acct, region, st, scanID, d) },
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, selectFields, perr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, selectFields, nil
}

func scanCasesDomains(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := connectcases.NewListDomainsPaginator(client, &connectcases.ListDomainsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "connectcases:ListDomains", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("connectcases:ListDomains: %w", err)
		}
		for _, d := range out.Domains {
			arn := sv(d.DomainArn)
			if arn == "" {
				continue
			}
			if id := sv(d.DomainId); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesDomain, NativeID: arn,
				Name: d.Name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "cases domains")
	return ids, t, i, err
}

func scanCasesCaseRules(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domainID string) (int, int, error) {
	did := domainID
	pager := connectcases.NewListCaseRulesPaginator(client, &connectcases.ListCaseRulesInput{DomainId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "connectcases:ListCaseRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("connectcases:ListCaseRules: %w", err)
		}
		for _, r := range out.CaseRules {
			arn := sv(r.CaseRuleArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesCaseRule, NativeID: arn,
				Name: r.Name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cases case-rules")
}

// casesSelectField is a custom single-select field of a domain, stored this
// scan.
type casesSelectField struct{ domainID, fieldID, arn string }

// scanCasesFields stores the domain's fields and returns its custom
// single-select ones. Single-select is the only type with options:
// BatchPutFieldOptions "creates and updates a set of field options for a
// single select field". System fields are excluded because their options are
// built in, not tenant configuration (the CreateLayout doc: "Title and Status
// fields ... are not configurable"; not observed live), and listing them
// would either store built-in options as resources in every domain or, if
// ListFieldOptions rejects system fields, fail the option phase on every scan.
func scanCasesFields(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domainID string) ([]casesSelectField, int, int, error) {
	did := domainID
	pager := connectcases.NewListFieldsPaginator(client, &connectcases.ListFieldsInput{DomainId: &did})
	var (
		batch        []*store.Resource
		selectFields []casesSelectField
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "connectcases:ListFields", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("connectcases:ListFields: %w", err)
		}
		for _, f := range out.Fields {
			arn := sv(f.FieldArn)
			if arn == "" {
				continue
			}
			if id := sv(f.FieldId); id != "" && f.Type == connectcasestypes.FieldTypeSingleSelect && f.Namespace == connectcasestypes.FieldNamespaceCustom {
				selectFields = append(selectFields, casesSelectField{domainID: domainID, fieldID: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesField, NativeID: arn,
				Name: f.Name, Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "cases fields")
	if err != nil {
		return nil, t, i, err
	}
	return selectFields, t, i, nil
}

const casesListFieldOptionsOp = "connectcases:ListFieldOptions"

// casesFieldOptionPageLimit is the model's MaxResults cap.
const casesFieldOptionPageLimit = 100

// casesFieldOptionNativeID keys an option under its field: options have no
// ARN, and the value is the option's key within its field (BatchPutFieldOptions
// creates or updates by it). The value goes last because its model pattern
// (^.*[\S]$) allows "/"; the field ARN before it carries the service-generated
// FieldId, so the "/option/" segment always marks where the value starts.
func casesFieldOptionNativeID(fieldARN, value string) string {
	return fieldARN + "/option/" + value
}

// scanCasesFieldOptions stores the options of each single-select field
// scanCasesFields stored, one row per option, each under its field. Per field:
// ResourceNotFoundException (field or domain deleted since listing) is skipped
// silently, a denial warns once for the op, a transient error after the SDK's
// retries is skipped with one warning for all such fields, and anything else
// fails the phase, storing none of its rows.
func scanCasesFieldOptions(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, fields []casesSelectField) (int, int, error) {
	parents := make([]childParent, len(fields))
	byARN := make(map[string]casesSelectField, len(fields))
	for i, f := range fields {
		parents[i] = childParent{id: f.arn, arn: f.arn}
		byARN[f.arn] = f
	}
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	return childFanOutSkipTransient(ctx, st, acct, region, casesListFieldOptionsOp, parents, isGone, byParentARN, func(ctx context.Context, p childParent) ([]*store.Resource, error) {
		return listCasesFieldOptions(ctx, client, acct, region, scanID, byARN[p.arn])
	})
}

func listCasesFieldOptions(ctx context.Context, client casesAPI, acct *account, region, scanID string, field casesSelectField) ([]*store.Resource, error) {
	pager := connectcases.NewListFieldOptionsPaginator(client,
		&connectcases.ListFieldOptionsInput{DomainId: &field.domainID, FieldId: &field.fieldID},
		func(o *connectcases.ListFieldOptionsPaginatorOptions) { o.Limit = casesFieldOptionPageLimit })
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range out.Options {
			value := sv(o.Value)
			if value == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesFieldOption, NativeID: casesFieldOptionNativeID(field.arn, value),
				Name: o.Name, Region: &region,
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}

func scanCasesLayouts(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domainID string) (int, int, error) {
	did := domainID
	pager := connectcases.NewListLayoutsPaginator(client, &connectcases.ListLayoutsInput{DomainId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "connectcases:ListLayouts", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("connectcases:ListLayouts: %w", err)
		}
		for _, l := range out.Layouts {
			arn := sv(l.LayoutArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesLayout, NativeID: arn,
				Name: l.Name, Region: &region,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cases layouts")
}

func scanCasesTemplates(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domainID string) (int, int, error) {
	did := domainID
	pager := connectcases.NewListTemplatesPaginator(client, &connectcases.ListTemplatesInput{DomainId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "connectcases:ListTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("connectcases:ListTemplates: %w", err)
		}
		for _, tmpl := range out.Templates {
			arn := sv(tmpl.TemplateArn)
			if arn == "" {
				continue
			}
			status := string(tmpl.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesTemplate, NativeID: arn,
				Name: tmpl.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(tmpl), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cases templates")
}
