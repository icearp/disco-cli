package aws

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
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
	ListTagsForResource(context.Context, *connectcases.ListTagsForResourceInput, ...func(*connectcases.Options)) (*connectcases.ListTagsForResourceOutput, error)
}

const (
	casesTagsOp             = "connectcases:ListTagsForResource"
	casesListFieldOptionsOp = "connectcases:ListFieldOptions"
	// casesDomainPageCap is the maxResults maximum of ListDomains.
	casesDomainPageCap = 10
	// casesPageCap is the maxResults maximum of every per-domain and
	// per-field listing read here.
	casesPageCap = 100
)

// casesGone is the not-found code ListTagsForResource models, and the one
// every per-domain and per-field listing models, for a resource or parent
// deleted since it was listed.
var casesGone = []string{"ResourceNotFoundException"}

// scanCases discovers the Connect Cases domains of one region, each domain's
// case rules, fields, layouts and templates, and the options of each custom
// single-select field. Every listing runs as its own phase, one at a time: a
// phase's error does not stop the later ones; a failed write and ctx's end
// do (runHostPhases). The child phases read the parents stored this scan,
// those parents concurrently. Every type but field options has its tags read
// per row (casesTags), whose denials are one warning for the whole scan.
func scanCases(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := connectcases.NewFromConfig(acct.cfg, func(o *connectcases.Options) { o.Region = region })
	tags := casesTags(client).sharingDenials()
	var (
		domains      []childParent
		selectFields []casesSelectField
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "cases", 0, []hostPhase{
		{op: "connectcases:ListDomains", run: func(p *phaseTotals) (failed bool) {
			domains, failed = scanCasesDomains(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "connectcases:ListCaseRules", run: func(p *phaseTotals) bool {
			return scanCasesCaseRules(ctx, client, acct, region, st, scanID, domains, tags, p)
		}},
		{op: "connectcases:ListFields", run: func(p *phaseTotals) (failed bool) {
			selectFields, failed = scanCasesFields(ctx, client, acct, region, st, scanID, domains, tags, p)
			return failed
		}},
		{op: "connectcases:ListLayouts", run: func(p *phaseTotals) bool {
			return scanCasesLayouts(ctx, client, acct, region, st, scanID, domains, tags, p)
		}},
		{op: "connectcases:ListTemplates", run: func(p *phaseTotals) bool {
			return scanCasesTemplates(ctx, client, acct, region, st, scanID, domains, tags, p)
		}},
		countedPhase(0, casesListFieldOptionsOp, func() (int, int, error) {
			return scanCasesFieldOptions(ctx, client, acct, region, st, scanID, selectFields)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// casesTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The Service Reference lists every
// type read here (CaseRule, Domain, Field, Layout, Template) under
// TagResource, and the Get or BatchGet response of each carries Tags; its
// ListTagsForResource entry names no resource type. Field options have no
// ARN and are not read. ResourceNotFoundException, the op's modeled
// not-found, drops a resource deleted after it was listed. The op's tag map
// holds pointer values; a nil one is stored as "", as kvTagsJSON stores it.
func casesTags(client casesAPI) rowTagReader {
	return rowTagReader{
		op:       casesTagsOp,
		notFound: casesGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &connectcases.ListTagsForResourceInput{Arn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(aws.ToStringMap(out.Tags)), nil
		},
	}
}

// casesRow is the row of a listed element stored under its ARN and named
// name; an element without an ARN gets no row, and arnRow's warning names
// such elements by what (e.g. "case rules").
func casesRow(acct *account, region, scanID, typ, what string, arn, name *string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn), Name: name, Region: &region,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanCasesDomains stores the domains with their tags, a page at a time, and
// returns the stored ones that have a DomainId, the parents of the per-domain
// phases. It reports whether a write failed, after which the scan must stop.
func scanCasesDomains(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "connectcases:ListDomains", "domains", "DomainId",
		func(ctx context.Context, token *string) ([]connectcasestypes.DomainSummary, *string, error) {
			out, err := client.ListDomains(ctx, &connectcases.ListDomainsInput{MaxResults: aws.Int32(casesDomainPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Domains, out.NextToken, nil
		},
		func(d connectcasestypes.DomainSummary) (*store.Resource, string, string) {
			return casesRow(acct, region, scanID, TypeCasesDomain, "domains", d.DomainArn, d.Name, d)
		},
		func(d connectcasestypes.DomainSummary) string { return sv(d.DomainId) },
		tags, p)
}

// scanCasesCaseRules stores each domain's case rules with their tags, each
// page written as soon as its tags are read (storeEachParentPagesTags); one
// domain's failure costs the others none of theirs.
func scanCasesCaseRules(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domains []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "connectcases:ListCaseRules", casesGone, domains,
		func(ctx context.Context, domainID string, token *string) ([]connectcasestypes.CaseRuleSummary, *string, error) {
			out, err := client.ListCaseRules(ctx, &connectcases.ListCaseRulesInput{DomainId: &domainID, MaxResults: aws.Int32(casesPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CaseRules, out.NextToken, nil
		},
		func(_ string, r connectcasestypes.CaseRuleSummary) (*store.Resource, string, string) {
			return casesRow(acct, region, scanID, TypeCasesCaseRule, "case rules", r.CaseRuleArn, r.Name, r)
		},
		tags, p)
}

// casesSelectField is a custom single-select field of a domain, stored this
// scan.
type casesSelectField struct{ domainID, fieldID, arn string }

// scanCasesFields stores each domain's fields with their tags, each page
// written as soon as its tags are read (storeEachParentPagesTagsWith); one
// domain's failure costs the others none of theirs. It returns the stored
// custom single-select fields, sorted by ARN, and reports whether a write
// failed, after which the scan must stop. A stored one without a FieldId is
// warned about, as stored without its options.
//
// Single-select is the only type with options: BatchPutFieldOptions "creates
// and updates a set of field options for a single select field". System
// fields are excluded because their options are built in, not tenant
// configuration (the CreateLayout doc: "Title and Status fields ... are not
// configurable"; not observed live), and listing them would either store
// built-in options as resources in every domain or, if ListFieldOptions
// rejects system fields, fail the option phase on every scan.
func scanCasesFields(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domains []childParent, tags rowTagReader, p *phaseTotals) ([]casesSelectField, bool) {
	const op = "connectcases:ListFields"
	var (
		mu       sync.Mutex // guards listed: the rows are built concurrently
		listed   = map[*store.Resource]casesSelectField{}
		selected []casesSelectField
		noID     skipTally
	)
	failed := storeEachParentPagesTagsWith(ctx, st, acct, region, op, casesGone, domains,
		func(ctx context.Context, domainID string, token *string) ([]connectcasestypes.FieldSummary, *string, error) {
			out, err := client.ListFields(ctx, &connectcases.ListFieldsInput{DomainId: &domainID, MaxResults: aws.Int32(casesPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Fields, out.NextToken, nil
		},
		func(domainID string, f connectcasestypes.FieldSummary) (*store.Resource, string, string) {
			r, why, key := casesRow(acct, region, scanID, TypeCasesField, "fields", f.FieldArn, f.Name, f)
			if r != nil && f.Type == connectcasestypes.FieldTypeSingleSelect && f.Namespace == connectcasestypes.FieldNamespaceCustom {
				mu.Lock()
				listed[r] = casesSelectField{domainID: domainID, fieldID: sv(f.FieldId), arn: r.NativeID}
				mu.Unlock()
			}
			return r, why, key
		},
		nil,
		func(rows []*store.Resource) {
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				f, ok := listed[r]
				switch {
				case !ok:
				case f.fieldID == "":
					noID.skippedLeast(r.NativeID, nil)
				default:
					selected = append(selected, f)
				}
				delete(listed, r)
			}
		},
		tags, p)
	if failed {
		return nil, true
	}
	warnNoChildID(st, op, acct, region, &noID, "single-select fields", "FieldId")
	// The domains' pages are written in no fixed order.
	slices.SortFunc(selected, func(a, b casesSelectField) int { return strings.Compare(a.arn, b.arn) })
	return selected, false
}

// casesFieldOptionNativeID keys an option under its field: options have no
// ARN, and the value is the option's key within its field (BatchPutFieldOptions
// creates or updates by it). The value goes last because its model pattern
// (^.*[\S]$) allows "/"; the field ARN before it carries the service-generated
// FieldId, so the "/option/" segment always marks where the value starts.
func casesFieldOptionNativeID(fieldARN, value string) string {
	return fieldARN + "/option/" + value
}

// scanCasesFieldOptions stores the options of each single-select field
// scanCasesFields stored, one row per option, each under its field. The
// fields are read concurrently and each is classified on its own
// (storeChildrenEachParent), so one field's failure costs the others none of
// their options and its own pages read before it are kept: a field or domain
// deleted since it was listed is skipped, a denial warns once for the op, and
// any other error, a transient one after the SDK's retries included, is
// returned after the rows are stored. Options carry no ARN, so no tags are
// read for them.
func scanCasesFieldOptions(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, fields []casesSelectField) (int, int, error) {
	parents := make([]childParent, len(fields))
	byARN := make(map[string]casesSelectField, len(fields))
	for i, f := range fields {
		parents[i] = childParent{id: f.fieldID, arn: f.arn}
		byARN[f.arn] = f
	}
	return storeChildrenEachParent(ctx, st, acct, region, casesListFieldOptionsOp, casesGone, parents, nil, nil,
		func(ctx context.Context, p childParent, token *string) ([]connectcasestypes.FieldOption, *string, error) {
			f := byARN[p.arn]
			out, err := client.ListFieldOptions(ctx, &connectcases.ListFieldOptionsInput{DomainId: &f.domainID, FieldId: &f.fieldID, MaxResults: aws.Int32(casesPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Options, out.NextToken, nil
		},
		func(p childParent, o connectcasestypes.FieldOption) (*store.Resource, string, string) {
			value := sv(o.Value)
			if value == "" {
				return nil, "field options have no value", cmp.Or(sv(o.Name), noIdentifier) + " in field " + p.arn
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCasesFieldOption, NativeID: casesFieldOptionNativeID(p.arn, value),
				Name: o.Name, Region: &region,
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanCasesLayouts stores each domain's layouts with their tags, each page
// written as soon as its tags are read (storeEachParentPagesTags); one
// domain's failure costs the others none of theirs.
func scanCasesLayouts(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domains []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "connectcases:ListLayouts", casesGone, domains,
		func(ctx context.Context, domainID string, token *string) ([]connectcasestypes.LayoutSummary, *string, error) {
			out, err := client.ListLayouts(ctx, &connectcases.ListLayoutsInput{DomainId: &domainID, MaxResults: aws.Int32(casesPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Layouts, out.NextToken, nil
		},
		func(_ string, l connectcasestypes.LayoutSummary) (*store.Resource, string, string) {
			return casesRow(acct, region, scanID, TypeCasesLayout, "layouts", l.LayoutArn, l.Name, l)
		},
		tags, p)
}

// scanCasesTemplates stores each domain's templates with their tags, each
// page written as soon as its tags are read (storeEachParentPagesTags); one
// domain's failure costs the others none of theirs.
func scanCasesTemplates(ctx context.Context, client casesAPI, acct *account, region string, st *store.Store, scanID string, domains []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "connectcases:ListTemplates", casesGone, domains,
		func(ctx context.Context, domainID string, token *string) ([]connectcasestypes.TemplateSummary, *string, error) {
			out, err := client.ListTemplates(ctx, &connectcases.ListTemplatesInput{DomainId: &domainID, MaxResults: aws.Int32(casesPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Templates, out.NextToken, nil
		},
		func(_ string, tmpl connectcasestypes.TemplateSummary) (*store.Resource, string, string) {
			r, why, key := casesRow(acct, region, scanID, TypeCasesTemplate, "templates", tmpl.TemplateArn, tmpl.Name, tmpl)
			if r != nil {
				status := string(tmpl.Status)
				r.Status = &status
			}
			return r, why, key
		},
		tags, p)
}
