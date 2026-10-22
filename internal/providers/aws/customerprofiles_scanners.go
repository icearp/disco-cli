package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/customerprofiles"
	cptypes "github.com/aws/aws-sdk-go-v2/service/customerprofiles/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCPDomain, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCPCalculatedAttributeDefinition, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCPEventStream, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCPEventTrigger, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCPIntegration, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCPObjectType, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCPRecommender, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCPSegmentDefinition, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCustomerProfilesLayouts, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCustomerProfilesDomainObjectTypes, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCustomerProfilesRecommenderFilters, Service: "customer-profiles"})
	registerType(restype.Descriptor{Type: TypeCustomerProfilesRecommenderSchemas, Service: "customer-profiles"})
	registerService(serviceEntry{
		name: "aws:customer-profiles",
		fn:   scanCustomerProfiles,
	})
}

type cpAPI interface {
	ListDomains(context.Context, *customerprofiles.ListDomainsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListDomainsOutput, error)
	GetDomain(context.Context, *customerprofiles.GetDomainInput, ...func(*customerprofiles.Options)) (*customerprofiles.GetDomainOutput, error)
	ListRecommenders(context.Context, *customerprofiles.ListRecommendersInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListRecommendersOutput, error)
	ListCalculatedAttributeDefinitions(context.Context, *customerprofiles.ListCalculatedAttributeDefinitionsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListCalculatedAttributeDefinitionsOutput, error)
	ListEventStreams(context.Context, *customerprofiles.ListEventStreamsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListEventStreamsOutput, error)
	ListEventTriggers(context.Context, *customerprofiles.ListEventTriggersInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListEventTriggersOutput, error)
	ListIntegrations(context.Context, *customerprofiles.ListIntegrationsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListIntegrationsOutput, error)
	ListProfileObjectTypes(context.Context, *customerprofiles.ListProfileObjectTypesInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListProfileObjectTypesOutput, error)
	ListSegmentDefinitions(context.Context, *customerprofiles.ListSegmentDefinitionsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListSegmentDefinitionsOutput, error)
	ListDomainLayouts(context.Context, *customerprofiles.ListDomainLayoutsInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListDomainLayoutsOutput, error)
	ListDomainObjectTypes(context.Context, *customerprofiles.ListDomainObjectTypesInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListDomainObjectTypesOutput, error)
	ListRecommenderFilters(context.Context, *customerprofiles.ListRecommenderFiltersInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListRecommenderFiltersOutput, error)
	ListRecommenderSchemas(context.Context, *customerprofiles.ListRecommenderSchemasInput, ...func(*customerprofiles.Options)) (*customerprofiles.ListRecommenderSchemasOutput, error)
}

func cpARN(region, acct string, segs ...string) string {
	s := fmt.Sprintf("arn:aws:profile:%s:%s:domains", region, acct)
	for _, seg := range segs {
		s += "/" + seg
	}
	return s
}

const (
	cpListDomainsOp = "profile:ListDomains"
	cpGetDomainOp   = "profile:GetDomain"
	// cpPageSize is the MaxResults cap the model sets on every list op here
	// except ListSegmentDefinitions, which allows cpSegmentPageSize.
	cpPageSize        = 100
	cpSegmentPageSize = 500
)

func scanCustomerProfiles(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := customerprofiles.NewFromConfig(acct.cfg, func(o *customerprofiles.Options) { o.Region = region })
	return scanCPAll(ctx, client, acct, region, st, scanID)
}

// scanCPAll stores the domains, then lists each child kind under every domain
// stored. Every phase runs whatever an earlier one returned, except after a
// failed store write; a phase that fails keeps the rows it read.
func scanCPAll(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var errs phaseErrs
	domains, total, inserted, werr := scanCPDomains(ctx, client, acct, region, st, scanID, &errs)
	if werr != nil {
		return total, inserted, errors.Join(errs.join(st, acct, region), werr)
	}
	for _, phase := range []struct {
		op   string
		scan func(context.Context, cpAPI, *account, string, *store.Store, string, []string) (int, int, error)
	}{
		{cpCalcAttrDefsOp, scanCPCalcAttrDefs},
		{cpEventStreamsOp, scanCPEventStreams},
		{cpEventTriggersOp, scanCPEventTriggers},
		{cpIntegrationsOp, scanCPIntegrations},
		{cpObjectTypesOp, scanCPObjectTypes},
		{cpSegmentDefinitionsOp, scanCPSegmentDefinitions},
		{cpRecommendersOp, scanCPRecommenders},
		{cpLayoutsOp, scanCPLayouts},
		{cpDomainObjectTypesOp, scanCPDomainObjectTypes},
		{cpRecommenderFiltersOp, scanCPRecommenderFilters},
		{cpRecommenderSchemasOp, scanCPRecommenderSchemas},
	} {
		t, i, ferr := phase.scan(ctx, client, acct, region, st, scanID, domains)
		total += t
		inserted += i
		errs.add(phase.op, ferr)
		if errors.Is(ferr, store.ErrStoreWrite) {
			break
		}
	}
	return total, inserted, errs.join(st, acct, region)
}

// cpDomainReads tallies the listed domains whose GetDomain read failed, one
// tally per cause so that each cause warns once, and the listed domains with
// no name.
type cpDomainReads struct {
	denied    skipTally
	transient transientTally
	unkeyed   skipTally
}

// scanCPDomains stores each listed domain (see cpDomainRow) and returns the
// names of the domains stored, for the child phases. A ListDomains page that
// fails keeps the domains read before it: a denial warns, any other error is
// filed in errs under ListDomains. GetDomain errors are filed under GetDomain.
// It returns only a failed write.
func scanCPDomains(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, errs *phaseErrs) ([]string, int, int, error) {
	var (
		names      []string
		batch      []*store.Resource
		reads      cpDomainReads
		listDenied error
		token      *string
	)
	for {
		out, err := client.ListDomains(ctx, &customerprofiles.ListDomainsInput{MaxResults: sdkaws.Int32(cpPageSize), NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				listDenied = err
			} else {
				errs.add(cpListDomainsOp, fmt.Errorf("%s: %w", cpListDomainsOp, err))
			}
			break
		}
		for _, d := range out.Items {
			row, rerr := cpDomainRow(ctx, client, acct, region, scanID, d, &reads)
			errs.add(cpGetDomainOp, rerr)
			if row != nil {
				batch = append(batch, row)
				names = append(names, sv(d.DomainName))
			}
		}
		last, terr := lastPage(token, out.NextToken)
		if terr != nil {
			errs.add(cpListDomainsOp, fmt.Errorf("%s: %w", cpListDomainsOp, terr))
		}
		if last {
			break
		}
		token = out.NextToken
	}
	total, inserted, uerr := upsertBatch(st, batch, "customer-profiles domains")
	if uerr != nil {
		return nil, total, inserted, uerr
	}
	if listDenied != nil {
		_ = skipIfAccessDenied(st, cpListDomainsOp, acct.ID, region, fmt.Errorf(
			"domains on this and later pages not stored: %w", listDenied))
	}
	const kept = "%d domain(s) stored as their ListDomains element instead of their GetDomain body, first %s: %w"
	if reads.denied.count > 0 {
		_ = skipIfAccessDenied(st, cpGetDomainOp, acct.ID, region, fmt.Errorf(kept, reads.denied.count, reads.denied.first, reads.denied.err))
	}
	if reads.transient.count > 0 {
		_ = skipIfTransient(st, cpGetDomainOp, acct.ID, region, fmt.Errorf(kept, reads.transient.count, reads.transient.first, reads.transient.err))
	}
	warnNotStored(st, cpListDomainsOp, acct, region, &reads.unkeyed, "domains have an empty DomainName")
	return names, total, inserted, nil
}

// cpDomainRow builds a listed domain's row from its GetDomain body. Tags come
// from the ListDomains element on every row, so a failed GetDomain does not
// drop them. A domain deleted since it was listed (ResourceNotFoundException)
// is not stored. A denied or still-throttled GetDomain keeps the ListDomains
// element and is tallied in reads; any other error keeps it too and is
// returned. A domain with no name is tallied as unkeyed.
func cpDomainRow(ctx context.Context, client cpAPI, acct *account, region, scanID string, d cptypes.ListDomainItem, reads *cpDomainReads) (*store.Resource, error) {
	name := sv(d.DomainName)
	if name == "" {
		reads.unkeyed.skipped("created "+sdkaws.ToTime(d.CreatedAt).Format(time.RFC3339), nil)
		return nil, nil
	}
	attrs := mustJSON(d)
	var err error
	gout, gerr := client.GetDomain(ctx, &customerprofiles.GetDomainInput{DomainName: &name})
	switch {
	case gerr == nil:
		attrs = mustJSON(gout)
	case isAPIErrorCode(gerr, "ResourceNotFoundException"):
		return nil, nil
	case isAccessDenied(gerr):
		reads.denied.skipped(name, gerr)
	case reads.transient.drop(name, gerr):
	default:
		err = fmt.Errorf("%s %s: %w", cpGetDomainOp, name, gerr)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeCPDomain, NativeID: cpARN(region, acct.ID, name),
		Name: &name, Region: &region, AttributesJSON: attrs,
		TagsJSON: mapTagsJSON(d.Tags), DiscoveredBy: scanID,
	}, err
}

// cpRow builds a domain child's row; the element is stored verbatim.
func cpRow(acct *account, region, scanID, typ, nativeID, name string, element any, tags map[string]string) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID, Name: &name, Region: &region,
		AttributesJSON: mustJSON(element), TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}
}

// cpListChildren lists one child kind under each domain through list, which
// fetches the page after token, and builds each item's row through row, which
// returns nil for an item with no identifier. A domain whose first page is
// ResourceNotFoundException was deleted after ListDomains listed it and is
// skipped. Any other failed page keeps the domain's rows read before it and
// moves on to the next domain: a denial warns once for the phase, every other
// error, a repeated NextToken included, is returned after storing. Warnings
// are recorded only after a successful write.
func cpListChildren[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what string, domains []string,
	list func(ctx context.Context, domain string, token *string) ([]Item, *string, error),
	row func(domain string, item Item) *store.Resource,
) (int, int, error) {
	var (
		batch           []*store.Resource
		errs            []error
		denied, unnamed skipTally
	)
	for _, domain := range domains {
		var token *string
		for {
			items, next, err := list(ctx, domain, token)
			if err != nil {
				switch {
				case token == nil && isAPIErrorCode(err, "ResourceNotFoundException"):
				case isAccessDenied(err):
					denied.skipped(domain, err)
				default:
					errs = append(errs, fmt.Errorf("domain %s: %w", domain, err))
				}
				break
			}
			for _, item := range items {
				if r := row(domain, item); r != nil {
					batch = append(batch, r)
				} else {
					unnamed.skipped("in domain "+domain, nil)
				}
			}
			last, terr := lastPage(token, next)
			if terr != nil {
				errs = append(errs, fmt.Errorf("domain %s: %w", domain, terr))
			}
			if last {
				break
			}
			token = next
		}
	}
	total, inserted, err := upsertListed(st, batch, op, errors.Join(errs...))
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf(
			"%d domain(s) not fully listed, %s read before the denial kept; first %s: %w", denied.count, what, denied.first, denied.err))
	}
	warnNotStored(st, op, acct, region, &unnamed, what+" have an empty identifier")
	return total, inserted, err
}

const (
	cpCalcAttrDefsOp       = "profile:ListCalculatedAttributeDefinitions"
	cpEventStreamsOp       = "profile:ListEventStreams"
	cpEventTriggersOp      = "profile:ListEventTriggers"
	cpIntegrationsOp       = "profile:ListIntegrations"
	cpObjectTypesOp        = "profile:ListProfileObjectTypes"
	cpSegmentDefinitionsOp = "profile:ListSegmentDefinitions"
	cpRecommendersOp       = "profile:ListRecommenders"
	cpLayoutsOp            = "profile:ListDomainLayouts"
	cpDomainObjectTypesOp  = "profile:ListDomainObjectTypes"
	cpRecommenderFiltersOp = "profile:ListRecommenderFilters"
	cpRecommenderSchemasOp = "profile:ListRecommenderSchemas"
)

func scanCPCalcAttrDefs(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpCalcAttrDefsOp, "calculated attribute definitions", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.ListCalculatedAttributeDefinitionItem, *string, error) {
			out, err := client.ListCalculatedAttributeDefinitions(ctx, &customerprofiles.ListCalculatedAttributeDefinitionsInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(domain string, c cptypes.ListCalculatedAttributeDefinitionItem) *store.Resource {
			name := sv(c.CalculatedAttributeName)
			if name == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCPCalculatedAttributeDefinition,
				cpARN(region, acct.ID, domain, "calculated-attributes", name), name, c, c.Tags)
		})
}

func scanCPEventStreams(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpEventStreamsOp, "event streams", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.EventStreamSummary, *string, error) {
			out, err := client.ListEventStreams(ctx, &customerprofiles.ListEventStreamsInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(_ string, e cptypes.EventStreamSummary) *store.Resource {
			arn := sv(e.EventStreamArn)
			if arn == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCPEventStream, arn, cmp.Or(sv(e.EventStreamName), arn), e, e.Tags)
		})
}

func scanCPEventTriggers(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpEventTriggersOp, "event triggers", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.EventTriggerSummaryItem, *string, error) {
			out, err := client.ListEventTriggers(ctx, &customerprofiles.ListEventTriggersInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(domain string, e cptypes.EventTriggerSummaryItem) *store.Resource {
			name := sv(e.EventTriggerName)
			if name == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCPEventTrigger,
				cpARN(region, acct.ID, domain, "event-triggers", name), name, e, e.Tags)
		})
}

// scanCPIntegrations leaves IncludeHidden unset, so the service's default
// (false) applies and hidden integrations are not listed.
func scanCPIntegrations(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpIntegrationsOp, "integrations", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.ListIntegrationItem, *string, error) {
			out, err := client.ListIntegrations(ctx, &customerprofiles.ListIntegrationsInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(domain string, it cptypes.ListIntegrationItem) *store.Resource {
			uri := sv(it.Uri)
			if uri == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCPIntegration,
				cpARN(region, acct.ID, domain, "integrations", uri), uri, it, it.Tags)
		})
}

func scanCPObjectTypes(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpObjectTypesOp, "object types", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.ListProfileObjectTypeItem, *string, error) {
			out, err := client.ListProfileObjectTypes(ctx, &customerprofiles.ListProfileObjectTypesInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(domain string, ot cptypes.ListProfileObjectTypeItem) *store.Resource {
			name := sv(ot.ObjectTypeName)
			if name == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCPObjectType,
				cpARN(region, acct.ID, domain, "object-types", name), name, ot, ot.Tags)
		})
}

func scanCPSegmentDefinitions(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpSegmentDefinitionsOp, "segment definitions", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.SegmentDefinitionItem, *string, error) {
			out, err := client.ListSegmentDefinitions(ctx, &customerprofiles.ListSegmentDefinitionsInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpSegmentPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(_ string, s cptypes.SegmentDefinitionItem) *store.Resource {
			arn := sv(s.SegmentDefinitionArn)
			if arn == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCPSegmentDefinition, arn, cmp.Or(sv(s.SegmentDefinitionName), arn), s, s.Tags)
		})
}

func scanCPRecommenders(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpRecommendersOp, "recommenders", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.RecommenderSummary, *string, error) {
			out, err := client.ListRecommenders(ctx, &customerprofiles.ListRecommendersInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Recommenders, out.NextToken, nil
		},
		func(domain string, r cptypes.RecommenderSummary) *store.Resource {
			name := sv(r.RecommenderName)
			if name == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCPRecommender,
				cpARN(region, acct.ID, domain, "recommenders", name), name, r, r.Tags)
		})
}

func scanCPLayouts(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpLayoutsOp, "layouts", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.LayoutItem, *string, error) {
			out, err := client.ListDomainLayouts(ctx, &customerprofiles.ListDomainLayoutsInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(domain string, l cptypes.LayoutItem) *store.Resource {
			name := sv(l.LayoutDefinitionName)
			if name == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCustomerProfilesLayouts,
				cpARN(region, acct.ID, domain, "layout", name), name, l, l.Tags)
		})
}

func scanCPDomainObjectTypes(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpDomainObjectTypesOp, "domain object types", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.DomainObjectTypesListItem, *string, error) {
			out, err := client.ListDomainObjectTypes(ctx, &customerprofiles.ListDomainObjectTypesInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(domain string, ot cptypes.DomainObjectTypesListItem) *store.Resource {
			name := sv(ot.ObjectTypeName)
			if name == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCustomerProfilesDomainObjectTypes,
				cpARN(region, acct.ID, domain, "object-type", name), name, ot, ot.Tags)
		})
}

func scanCPRecommenderFilters(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpRecommenderFiltersOp, "recommender filters", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.RecommenderFilterSummary, *string, error) {
			out, err := client.ListRecommenderFilters(ctx, &customerprofiles.ListRecommenderFiltersInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.RecommenderFilters, out.NextToken, nil
		},
		func(domain string, f cptypes.RecommenderFilterSummary) *store.Resource {
			name := sv(f.RecommenderFilterName)
			if name == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCustomerProfilesRecommenderFilters,
				cpARN(region, acct.ID, domain, "recommender-filter", name), name, f, f.Tags)
		})
}

// scanCPRecommenderSchemas stores no tags: RecommenderSchemaSummary carries
// none, and ListTagsForResource is not called.
func scanCPRecommenderSchemas(ctx context.Context, client cpAPI, acct *account, region string, st *store.Store, scanID string, domains []string) (int, int, error) {
	return cpListChildren(ctx, st, acct, region, cpRecommenderSchemasOp, "recommender schemas", domains,
		func(ctx context.Context, domain string, token *string) ([]cptypes.RecommenderSchemaSummary, *string, error) {
			out, err := client.ListRecommenderSchemas(ctx, &customerprofiles.ListRecommenderSchemasInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(cpPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.RecommenderSchemas, out.NextToken, nil
		},
		func(domain string, s cptypes.RecommenderSchemaSummary) *store.Resource {
			name := sv(s.RecommenderSchemaName)
			if name == "" {
				return nil
			}
			return cpRow(acct, region, scanID, TypeCustomerProfilesRecommenderSchemas,
				cpARN(region, acct.ID, domain, "recommender-schema", name), name, s, nil)
		})
}
