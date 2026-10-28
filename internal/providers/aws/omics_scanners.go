package aws

import (
	"cmp"
	"context"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/omics"
	omicstypes "github.com/aws/aws-sdk-go-v2/service/omics/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOmicsAnnotationStore, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsConfiguration, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsReferenceStore, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsRunGroup, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsSequenceStore, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsVariantStore, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsWorkflow, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsWorkflowVersion, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsAnnotationStoreVersion, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsReference, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsRunCache, Service: "omics"})
	registerType(restype.Descriptor{Type: TypeOmicsShare, Service: "omics"})
	registerService(serviceEntry{
		name: "aws:omics",
		fn:   scanOmics,
	})
}

type omicsAPI interface {
	ListAnnotationStores(context.Context, *omics.ListAnnotationStoresInput, ...func(*omics.Options)) (*omics.ListAnnotationStoresOutput, error)
	ListConfigurations(context.Context, *omics.ListConfigurationsInput, ...func(*omics.Options)) (*omics.ListConfigurationsOutput, error)
	ListReferenceStores(context.Context, *omics.ListReferenceStoresInput, ...func(*omics.Options)) (*omics.ListReferenceStoresOutput, error)
	ListRunGroups(context.Context, *omics.ListRunGroupsInput, ...func(*omics.Options)) (*omics.ListRunGroupsOutput, error)
	ListSequenceStores(context.Context, *omics.ListSequenceStoresInput, ...func(*omics.Options)) (*omics.ListSequenceStoresOutput, error)
	ListVariantStores(context.Context, *omics.ListVariantStoresInput, ...func(*omics.Options)) (*omics.ListVariantStoresOutput, error)
	ListWorkflows(context.Context, *omics.ListWorkflowsInput, ...func(*omics.Options)) (*omics.ListWorkflowsOutput, error)
	ListWorkflowVersions(context.Context, *omics.ListWorkflowVersionsInput, ...func(*omics.Options)) (*omics.ListWorkflowVersionsOutput, error)
	ListAnnotationStoreVersions(context.Context, *omics.ListAnnotationStoreVersionsInput, ...func(*omics.Options)) (*omics.ListAnnotationStoreVersionsOutput, error)
	ListReferences(context.Context, *omics.ListReferencesInput, ...func(*omics.Options)) (*omics.ListReferencesOutput, error)
	ListRunCaches(context.Context, *omics.ListRunCachesInput, ...func(*omics.Options)) (*omics.ListRunCachesOutput, error)
	ListShares(context.Context, *omics.ListSharesInput, ...func(*omics.Options)) (*omics.ListSharesOutput, error)
	ListTagsForResource(context.Context, *omics.ListTagsForResourceInput, ...func(*omics.Options)) (*omics.ListTagsForResourceOutput, error)
}

const omicsListTagsOp = "omics:ListTagsForResource"

// omicsNotFound is the code ListTagsForResource and the per-store and
// per-workflow listings model for a resource deleted since it was listed.
var omicsNotFound = []string{"ResourceNotFoundException"}

// The hosts the listings call, from each op's EndpointHostPrefix in the SDK:
// workflows-, control-storage- and analytics-. ListTagsForResource calls a
// fourth, tags-, for every type; it runs only after a listing returned rows.
const (
	omicsWorkflowsHost = iota
	omicsStorageHost
	omicsAnalyticsHost
)

// Each listing sends the maximum of its op's maxResults range trait, 10 for
// ListConfigurations and 100 for the others; the docs give no range of their
// own. ListShares' maxResults has neither, so it is not sent.
const (
	omicsPageCap              = 100
	omicsConfigurationPageCap = 10
)

func scanOmics(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := omics.NewFromConfig(acct.cfg, func(o *omics.Options) { o.Region = region })
	return scanOmicsIn(ctx, client, acct, region, st, scanID)
}

// scanOmicsIn runs every listing in turn, each per-store or per-workflow
// listing after the listing of its parents, which hands it the parents it
// stored. A phase's error does not stop the later ones; a failed write and
// ctx's end do (runHostPhases). The phases run one at a time, so readEachKey's
// own bound is the bound on the tag reads in flight. A region that does not
// offer HealthOmics is reported unavailable when a listing says so before any
// listing answered with an item or an error (omicsGate).
func scanOmicsIn(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		gate                                         omicsGate
		tags                                         = omicsTags(client, acct.ID)
		annotationStores, referenceStores, workflows []childParent
		workflowTypes                                map[string]omicstypes.WorkflowType
		storeWriteFailed                             bool
		phase                                        = func(host int, op string, run func(p *phaseTotals) bool) hostPhase {
			return hostPhase{op: op, host: host, run: func(p *phaseTotals) bool {
				if gate.err != nil {
					return false
				}
				return run(p)
			}}
		}
	)
	phases := []hostPhase{
		phase(omicsWorkflowsHost, "omics:ListConfigurations", func(p *phaseTotals) bool {
			return scanOmicsConfigurations(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase(omicsWorkflowsHost, "omics:ListRunGroups", func(p *phaseTotals) bool {
			return scanOmicsRunGroups(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase(omicsStorageHost, "omics:ListSequenceStores", func(p *phaseTotals) bool {
			return scanOmicsSequenceStores(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase(omicsAnalyticsHost, "omics:ListVariantStores", func(p *phaseTotals) bool {
			return scanOmicsVariantStores(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase(omicsWorkflowsHost, "omics:ListRunCaches", func(p *phaseTotals) bool {
			return scanOmicsRunCaches(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase(omicsAnalyticsHost, "omics:ListAnnotationStores", func(p *phaseTotals) bool {
			annotationStores, storeWriteFailed = scanOmicsAnnotationStores(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return storeWriteFailed
		}),
		phase(omicsAnalyticsHost, "omics:ListAnnotationStoreVersions", func(p *phaseTotals) bool {
			return scanOmicsAnnotationStoreVersions(ctx, client, acct, region, st, scanID, annotationStores, tags, p)
		}),
		phase(omicsStorageHost, "omics:ListReferenceStores", func(p *phaseTotals) bool {
			referenceStores, storeWriteFailed = scanOmicsReferenceStores(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return storeWriteFailed
		}),
		phase(omicsStorageHost, "omics:ListReferences", func(p *phaseTotals) bool {
			return scanOmicsReferences(ctx, client, acct, region, st, scanID, referenceStores, tags, p)
		}),
		phase(omicsWorkflowsHost, "omics:ListWorkflows", func(p *phaseTotals) bool {
			workflows, workflowTypes, storeWriteFailed = scanOmicsWorkflows(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return storeWriteFailed
		}),
		phase(omicsWorkflowsHost, "omics:ListWorkflowVersions", func(p *phaseTotals) bool {
			return scanOmicsWorkflowVersions(ctx, client, acct, region, st, scanID, workflows, workflowTypes, tags, p)
		}),
		phase(omicsAnalyticsHost, "omics:ListShares", func(p *phaseTotals) bool {
			t, i, err := scanOmicsShares(ctx, client, acct, region, st, scanID, &gate)
			return p.addPhase("omics:ListShares", t, i, err)
		}),
	}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "omics", omicsWorkflowsHost, phases)
	if gate.err != nil {
		return total, inserted, markServiceUnavailable(gate.err)
	}
	return total, inserted, err
}

// omicsGate records the answer of a region that does not offer HealthOmics
// (isServiceNotAvailableInRegion) to an account-wide listing's first page
// while every page read before it answered empty and without an error
// (answered stays false): the scan then reports the region unavailable and no
// later listing runs. Nothing was stored, failed or warned about by then,
// since only a listed item or an error leads to a row, an error or a warning.
// The same answer after an item or an error, or on a later page, follows
// answers that show the service served here, and is handled as the error it
// is coded as: skipIfAccessDenied drops the AccessDeniedException form
// without a warning, and the NotFoundException form is returned.
type omicsGate struct {
	err      error
	answered bool
}

// omicsListPage reads one page of an account-wide listing with read, ending
// the listing without an error on the answer gate records.
func omicsListPage[Item any](gate *omicsGate, read func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := read(ctx, token)
		if err != nil && token == nil && !gate.answered && isServiceNotAvailableInRegion(err) {
			gate.err = err
			return nil, nil, nil
		}
		if err != nil || len(items) > 0 {
			gate.answered = true
		}
		return items, next, err
	}
}

// omicsChildPages reads a per-parent listing's pages with read, which is
// handed the parent's key and, for a parent another account owns, that
// account (else ""). A not-found answer for such a parent ends its listing,
// keeping the pages read, and is tallied in gone rather than returned: the
// listing just returned the parent, so the answer more likely says the
// caller cannot list that account's children than that the parent was
// deleted, and the caller warns about it once the rows are written. An own parent's not-found is
// returned, for storeEachParentTags to skip as deleted.
func omicsChildPages[Item any](acctID string, parents []childParent, gone *skipTally,
	read func(ctx context.Context, key, owner string, token *string) ([]Item, *string, error),
) func(ctx context.Context, key string, token *string) ([]Item, *string, error) {
	type owner struct {
		other   bool
		account string
	}
	owners := make(map[string]owner, len(parents))
	for _, p := range parents {
		if a, ok := parseARN(p.arn); ok && a.AccountID != acctID {
			owners[p.id] = owner{true, a.AccountID}
		}
	}
	return func(ctx context.Context, key string, token *string) ([]Item, *string, error) {
		o := owners[key]
		items, next, err := read(ctx, key, o.account, token)
		if o.other && isAPIErrorCode(err, omicsNotFound...) {
			gone.skipped(key, err)
			return nil, nil, nil
		}
		return items, next, err
	}
}

// omicsWarnOtherOwnerGone warns about the parents omicsChildPages tallied in
// gone, unless the write failed; why says what the op's answer may mean.
func omicsWarnOtherOwnerGone(st *store.Store, op string, acct *account, region string, gone *skipTally, storeWriteFailed bool, why string) {
	if storeWriteFailed {
		return
	}
	gone.warnCode(st, op, acct, region, "ResourceNotFoundException", "parent(s) another account owns", why)
}

// omicsTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries; the op answers with every tag
// at once (no page token). Its model lists AccessDeniedException as its only
// denial code. The Service Reference names no resource for
// ListTagsForResource and every tagged type under TagResource. The model
// caps resourceArn at 150 characters, below the caps of configuration (190)
// and annotation store, annotation store version and variant store (2048)
// ARNs; those rows are read too, and whether the op refuses a longer ARN is
// not verified live: a refusal is a hard error with the row kept.
//
// A row another account owns is read too, under otherAccountGap. Such rows
// can exist: the model shares variant stores, annotation stores and
// workflows with other accounts (ShareResourceType, CreateShare), and
// ListWorkflows lists AWS's Ready2Run workflows; whether the listings return
// either is not verified.
func omicsTags(client omicsAPI, acctID string) rowTagReader {
	return rowTagReader{
		op:       omicsListTagsOp,
		notFound: omicsNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &omics.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if otherAccountGap(r.NativeID, acctID, err, omicsNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// omicsRow is the row of a listed item stored under its ARN, item verbatim as
// the attributes.
func omicsRow(acct *account, region, scanID, typ, arn, name string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// omicsNoARN is the reason and key of a listed item not stored because it
// lacks the ARN its NativeID is.
func omicsNoARN(what, key string) (*store.Resource, string, string) {
	return nil, what + " have an empty ARN", cmp.Or(key, "(an element with no name or id)")
}

func scanOmicsConfigurations(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, gate *omicsGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "omics:ListConfigurations",
		omicsListPage(gate, func(ctx context.Context, token *string) ([]omicstypes.ConfigurationListItem, *string, error) {
			out, err := client.ListConfigurations(ctx, &omics.ListConfigurationsInput{StartingToken: token, MaxResults: sdkaws.Int32(omicsConfigurationPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(c omicstypes.ConfigurationListItem) (*store.Resource, string, string) {
			arn := sv(c.Arn)
			if arn == "" {
				return omicsNoARN("configurations", sv(c.Name))
			}
			return omicsRow(acct, region, scanID, TypeOmicsConfiguration, arn, cmp.Or(sv(c.Name), arn), c), "", ""
		},
		tags, p)
}

func scanOmicsRunGroups(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, gate *omicsGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "omics:ListRunGroups",
		omicsListPage(gate, func(ctx context.Context, token *string) ([]omicstypes.RunGroupListItem, *string, error) {
			out, err := client.ListRunGroups(ctx, &omics.ListRunGroupsInput{StartingToken: token, MaxResults: sdkaws.Int32(omicsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(rg omicstypes.RunGroupListItem) (*store.Resource, string, string) {
			arn := sv(rg.Arn)
			if arn == "" {
				return omicsNoARN("run groups", cmp.Or(sv(rg.Name), sv(rg.Id)))
			}
			return omicsRow(acct, region, scanID, TypeOmicsRunGroup, arn, cmp.Or(sv(rg.Name), sv(rg.Id)), rg), "", ""
		},
		tags, p)
}

func scanOmicsSequenceStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, gate *omicsGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "omics:ListSequenceStores",
		omicsListPage(gate, func(ctx context.Context, token *string) ([]omicstypes.SequenceStoreDetail, *string, error) {
			out, err := client.ListSequenceStores(ctx, &omics.ListSequenceStoresInput{NextToken: token, MaxResults: sdkaws.Int32(omicsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.SequenceStores, out.NextToken, nil
		}),
		func(s omicstypes.SequenceStoreDetail) (*store.Resource, string, string) {
			arn := sv(s.Arn)
			if arn == "" {
				return omicsNoARN("sequence stores", cmp.Or(sv(s.Name), sv(s.Id)))
			}
			return omicsRow(acct, region, scanID, TypeOmicsSequenceStore, arn, cmp.Or(sv(s.Name), sv(s.Id)), s), "", ""
		},
		tags, p)
}

func scanOmicsVariantStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, gate *omicsGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "omics:ListVariantStores",
		omicsListPage(gate, func(ctx context.Context, token *string) ([]omicstypes.VariantStoreItem, *string, error) {
			out, err := client.ListVariantStores(ctx, &omics.ListVariantStoresInput{NextToken: token, MaxResults: sdkaws.Int32(omicsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.VariantStores, out.NextToken, nil
		}),
		func(s omicstypes.VariantStoreItem) (*store.Resource, string, string) {
			arn := sv(s.StoreArn)
			if arn == "" {
				return omicsNoARN("variant stores", cmp.Or(sv(s.Name), sv(s.Id)))
			}
			return omicsRow(acct, region, scanID, TypeOmicsVariantStore, arn, cmp.Or(sv(s.Name), sv(s.Id)), s), "", ""
		},
		tags, p)
}

func scanOmicsRunCaches(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, gate *omicsGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "omics:ListRunCaches",
		omicsListPage(gate, func(ctx context.Context, token *string) ([]omicstypes.RunCacheListItem, *string, error) {
			out, err := client.ListRunCaches(ctx, &omics.ListRunCachesInput{StartingToken: token, MaxResults: sdkaws.Int32(omicsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(c omicstypes.RunCacheListItem) (*store.Resource, string, string) {
			arn := sv(c.Arn)
			if arn == "" {
				return omicsNoARN("run caches", cmp.Or(sv(c.Name), sv(c.Id)))
			}
			return omicsRow(acct, region, scanID, TypeOmicsRunCache, arn, cmp.Or(sv(c.Name), sv(c.Id)), c), "", ""
		},
		tags, p)
}

// scanOmicsAnnotationStores stores the annotation stores with their tags and
// returns the stored ones keyed by Name, which ListAnnotationStoreVersions
// requires.
func scanOmicsAnnotationStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, gate *omicsGate, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "omics:ListAnnotationStores", "annotation stores", "Name",
		omicsListPage(gate, func(ctx context.Context, token *string) ([]omicstypes.AnnotationStoreItem, *string, error) {
			out, err := client.ListAnnotationStores(ctx, &omics.ListAnnotationStoresInput{NextToken: token, MaxResults: sdkaws.Int32(omicsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AnnotationStores, out.NextToken, nil
		}),
		func(s omicstypes.AnnotationStoreItem) (*store.Resource, string, string) {
			arn := sv(s.StoreArn)
			if arn == "" {
				return omicsNoARN("annotation stores", cmp.Or(sv(s.Name), sv(s.Id)))
			}
			return omicsRow(acct, region, scanID, TypeOmicsAnnotationStore, arn, cmp.Or(sv(s.Name), sv(s.Id)), s), "", ""
		},
		func(s omicstypes.AnnotationStoreItem) string { return sv(s.Name) },
		tags, p)
}

// scanOmicsAnnotationStoreVersions stores the versions of each annotation
// store stored this scan, with their tags; the resolver wires each to its
// store via StoreId. The op takes no owner input, so a store another account
// owns is listed by name alone.
func scanOmicsAnnotationStoreVersions(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, stores []childParent, tags rowTagReader, p *phaseTotals) bool {
	const op = "omics:ListAnnotationStoreVersions"
	var gone skipTally
	_, storeWriteFailed := storeEachParentTags(ctx, st, acct, region, op, omicsNotFound, stores,
		omicsChildPages(acct.ID, stores, &gone, func(ctx context.Context, name, _ string, token *string) ([]omicstypes.AnnotationStoreVersionItem, *string, error) {
			out, err := client.ListAnnotationStoreVersions(ctx, &omics.ListAnnotationStoreVersionsInput{Name: sdkaws.String(name), NextToken: token, MaxResults: sdkaws.Int32(omicsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AnnotationStoreVersions, out.NextToken, nil
		}),
		func(name string, v omicstypes.AnnotationStoreVersionItem) (*store.Resource, string, string) {
			arn := sv(v.VersionArn)
			if arn == "" {
				return omicsNoARN("annotation store versions", cmp.Or(sv(v.VersionName), "(a version with no name)")+" of store "+name)
			}
			return omicsRow(acct, region, scanID, TypeOmicsAnnotationStoreVersion, arn, cmp.Or(sv(v.VersionName), arn), v), "", ""
		},
		tags, p)
	omicsWarnOtherOwnerGone(st, op, acct, region, &gone, storeWriteFailed,
		"the store listing had just returned each, and the op takes no owner input, so it may not list another account's store")
	return storeWriteFailed
}

// scanOmicsReferenceStores stores the reference stores with their tags and
// returns the stored ones keyed by Id, which ListReferences requires.
func scanOmicsReferenceStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, gate *omicsGate, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "omics:ListReferenceStores", "reference stores", "Id",
		omicsListPage(gate, func(ctx context.Context, token *string) ([]omicstypes.ReferenceStoreDetail, *string, error) {
			out, err := client.ListReferenceStores(ctx, &omics.ListReferenceStoresInput{NextToken: token, MaxResults: sdkaws.Int32(omicsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ReferenceStores, out.NextToken, nil
		}),
		func(r omicstypes.ReferenceStoreDetail) (*store.Resource, string, string) {
			arn := sv(r.Arn)
			if arn == "" {
				return omicsNoARN("reference stores", cmp.Or(sv(r.Name), sv(r.Id)))
			}
			return omicsRow(acct, region, scanID, TypeOmicsReferenceStore, arn, cmp.Or(sv(r.Name), sv(r.Id)), r), "", ""
		},
		func(r omicstypes.ReferenceStoreDetail) string { return sv(r.Id) },
		tags, p)
}

// scanOmicsReferences stores the references of each reference store stored
// this scan, with their tags; the resolver wires each to its store via
// ReferenceStoreId.
func scanOmicsReferences(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, stores []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, storeWriteFailed := storeEachParentTags(ctx, st, acct, region, "omics:ListReferences", omicsNotFound, stores,
		func(ctx context.Context, id string, token *string) ([]omicstypes.ReferenceListItem, *string, error) {
			out, err := client.ListReferences(ctx, &omics.ListReferencesInput{ReferenceStoreId: sdkaws.String(id), NextToken: token, MaxResults: sdkaws.Int32(omicsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.References, out.NextToken, nil
		},
		func(id string, ref omicstypes.ReferenceListItem) (*store.Resource, string, string) {
			arn := sv(ref.Arn)
			if arn == "" {
				return omicsNoARN("references", cmp.Or(sv(ref.Name), sv(ref.Id), "(a reference with no name or id)")+" of store "+id)
			}
			return omicsRow(acct, region, scanID, TypeOmicsReference, arn, cmp.Or(sv(ref.Name), sv(ref.Id)), ref), "", ""
		},
		tags, p)
	return storeWriteFailed
}

// scanOmicsWorkflows stores the workflows with their tags and returns the
// stored ones keyed by Id, which ListWorkflowVersions requires, with each
// listed workflow's Type by Id.
func scanOmicsWorkflows(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, gate *omicsGate, tags rowTagReader, p *phaseTotals) ([]childParent, map[string]omicstypes.WorkflowType, bool) {
	types := map[string]omicstypes.WorkflowType{}
	parents, storeWriteFailed := storeParentListTags(ctx, st, acct, region, "omics:ListWorkflows", "workflows", "Id",
		omicsListPage(gate, func(ctx context.Context, token *string) ([]omicstypes.WorkflowListItem, *string, error) {
			out, err := client.ListWorkflows(ctx, &omics.ListWorkflowsInput{StartingToken: token, MaxResults: sdkaws.Int32(omicsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(w omicstypes.WorkflowListItem) (*store.Resource, string, string) {
			arn := sv(w.Arn)
			if arn == "" {
				return omicsNoARN("workflows", cmp.Or(sv(w.Name), sv(w.Id)))
			}
			return omicsRow(acct, region, scanID, TypeOmicsWorkflow, arn, cmp.Or(sv(w.Name), sv(w.Id)), w), "", ""
		},
		func(w omicstypes.WorkflowListItem) string {
			types[sv(w.Id)] = w.Type
			return sv(w.Id)
		},
		tags, p)
	return parents, types, storeWriteFailed
}

// scanOmicsWorkflowVersions stores the versions of each workflow stored this
// scan, with their tags; the resolver wires each to its workflow via
// WorkflowId. A workflow whose Type is not PRIVATE (a Ready2Run one) is sent
// its Type, which the op's docs say identifies it; a private one is sent
// none, the request the scanner always sent for it, since whether the op
// accepts a Type there is not verified live. A workflow another account owns
// is sent its owner's account, which the docs ask for unless the caller owns
// the workflow, except a Ready2Run one: whether the op accepts the AWS-owned
// account of its ARN is not verified, so its Type alone identifies it.
func scanOmicsWorkflowVersions(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, workflows []childParent, types map[string]omicstypes.WorkflowType, tags rowTagReader, p *phaseTotals) bool {
	const op = "omics:ListWorkflowVersions"
	var gone skipTally
	_, storeWriteFailed := storeEachParentTags(ctx, st, acct, region, op, omicsNotFound, workflows,
		omicsChildPages(acct.ID, workflows, &gone, func(ctx context.Context, id, owner string, token *string) ([]omicstypes.WorkflowVersionListItem, *string, error) {
			in := &omics.ListWorkflowVersionsInput{WorkflowId: sdkaws.String(id), StartingToken: token, MaxResults: sdkaws.Int32(omicsPageCap)}
			if t := types[id]; t != "" && t != omicstypes.WorkflowTypePrivate {
				in.Type = t
			}
			if owner != "" && types[id] != omicstypes.WorkflowTypeReady2run {
				in.WorkflowOwnerId = sdkaws.String(owner)
			}
			out, err := client.ListWorkflowVersions(ctx, in)
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(id string, v omicstypes.WorkflowVersionListItem) (*store.Resource, string, string) {
			arn := sv(v.Arn)
			if arn == "" {
				return omicsNoARN("workflow versions", cmp.Or(sv(v.VersionName), "(a version with no name)")+" of workflow "+id)
			}
			return omicsRow(acct, region, scanID, TypeOmicsWorkflowVersion, arn, cmp.Or(sv(v.VersionName), arn), v), "", ""
		},
		tags, p)
	omicsWarnOtherOwnerGone(st, op, acct, region, &gone, storeWriteFailed,
		"the workflow listing had just returned each, so the caller may not be allowed to list another account's workflow versions")
	return storeWriteFailed
}

// scanOmicsShares lists the resource shares this account owns. Shares owned by
// another account (ResourceOwner OTHER) are not stored: they are that account's
// resources, inbound here (same rule as scanRAM). ShareDetails carries no ARN
// and the Service Reference defines none, so NativeID is
// {ResourceArn}/share/{ShareId}, and a share has no tags to read.
func scanOmicsShares(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string, gate *omicsGate) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "omics:ListShares",
		omicsListPage(gate, func(ctx context.Context, token *string) ([]omicstypes.ShareDetails, *string, error) {
			out, err := client.ListShares(ctx, &omics.ListSharesInput{ResourceOwner: omicstypes.ResourceOwnerSelf, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			// A revoked share is not a live resource, and the API's Status
			// filter is inclusion-only, so DELETED shares are dropped here.
			return slices.DeleteFunc(out.Shares, func(s omicstypes.ShareDetails) bool {
				return s.Status == omicstypes.ShareStatusDeleted
			}), out.NextToken, nil
		}),
		func(s omicstypes.ShareDetails) (*store.Resource, string, string) {
			resourceARN, shareID := sv(s.ResourceArn), sv(s.ShareId)
			if resourceARN == "" || shareID == "" {
				return nil, "shares have an empty ResourceArn or ShareId", cmp.Or(shareID, resourceARN, sv(s.ShareName), "(a share with no id)")
			}
			r := omicsRow(acct, region, scanID, TypeOmicsShare, resourceARN+"/share/"+shareID, cmp.Or(sv(s.ShareName), shareID), s)
			r.CreatedAt = tp(s.CreationTime)
			if status := string(s.Status); status != "" {
				r.Status = &status
			}
			return r, "", ""
		})
}
