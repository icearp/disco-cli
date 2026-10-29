package aws

import (
	"cmp"
	"context"
	"errors"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// ssmParameterARN builds a parameter's NativeID from its name. Names may lack
// a leading "/", and the ARN never doubles it: "/a/b" and "a" become
// parameter/a/b and parameter/a.
func ssmParameterARN(partition, region, acct, name string) string {
	return arnFor(partition, "ssm", region, acct, "parameter/"+strings.TrimPrefix(name, "/"))
}

// ssmMaintenanceWindowARN builds a maintenance window's NativeID from its id;
// the API returns no ARN, and the target and task resolvers rebuild it.
func ssmMaintenanceWindowARN(partition, region, acct, windowID string) string {
	return arnFor(partition, "ssm", region, acct, "maintenancewindow/"+windowID)
}

func init() {
	registerType(restype.Descriptor{Type: TypeSSMDocument, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMParameter, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMPatchBaseline, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMAssociation, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMMaintenanceWindow, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMMaintenanceWindowTarget, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMMaintenanceWindowTask, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMResourceDataSync, Service: "ssm", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeSSMManagedInstance, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMOpsMetadata, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMActivation, Service: "ssm"})
	registerType(restype.Descriptor{Type: TypeSSMCloudConnector, Service: "ssm"})
	registerService(serviceEntry{
		name: "aws:ssm",
		fn:   scanSSM,
	})
}

// ssmAPI is the narrow set of SSM operations called by scanSSMAll.
type ssmAPI interface {
	DescribeParameters(context.Context, *ssm.DescribeParametersInput, ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error)
	ListDocuments(context.Context, *ssm.ListDocumentsInput, ...func(*ssm.Options)) (*ssm.ListDocumentsOutput, error)
	DescribeDocument(context.Context, *ssm.DescribeDocumentInput, ...func(*ssm.Options)) (*ssm.DescribeDocumentOutput, error)
	DescribePatchBaselines(context.Context, *ssm.DescribePatchBaselinesInput, ...func(*ssm.Options)) (*ssm.DescribePatchBaselinesOutput, error)
	ListAssociations(context.Context, *ssm.ListAssociationsInput, ...func(*ssm.Options)) (*ssm.ListAssociationsOutput, error)
	DescribeMaintenanceWindows(context.Context, *ssm.DescribeMaintenanceWindowsInput, ...func(*ssm.Options)) (*ssm.DescribeMaintenanceWindowsOutput, error)
	DescribeMaintenanceWindowTargets(context.Context, *ssm.DescribeMaintenanceWindowTargetsInput, ...func(*ssm.Options)) (*ssm.DescribeMaintenanceWindowTargetsOutput, error)
	DescribeMaintenanceWindowTasks(context.Context, *ssm.DescribeMaintenanceWindowTasksInput, ...func(*ssm.Options)) (*ssm.DescribeMaintenanceWindowTasksOutput, error)
	ListResourceDataSync(context.Context, *ssm.ListResourceDataSyncInput, ...func(*ssm.Options)) (*ssm.ListResourceDataSyncOutput, error)
	DescribeInstanceInformation(context.Context, *ssm.DescribeInstanceInformationInput, ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
	ListOpsMetadata(context.Context, *ssm.ListOpsMetadataInput, ...func(*ssm.Options)) (*ssm.ListOpsMetadataOutput, error)
	DescribeActivations(context.Context, *ssm.DescribeActivationsInput, ...func(*ssm.Options)) (*ssm.DescribeActivationsOutput, error)
	ListCloudConnectors(context.Context, *ssm.ListCloudConnectorsInput, ...func(*ssm.Options)) (*ssm.ListCloudConnectorsOutput, error)
	DescribeDocumentPermission(context.Context, *ssm.DescribeDocumentPermissionInput, ...func(*ssm.Options)) (*ssm.DescribeDocumentPermissionOutput, error)
	ListTagsForResource(context.Context, *ssm.ListTagsForResourceInput, ...func(*ssm.Options)) (*ssm.ListTagsForResourceOutput, error)
}

const (
	ssmDescribeParametersOp               = "ssm:DescribeParameters"
	ssmListDocumentsOp                    = "ssm:ListDocuments"
	ssmDescribeDocumentOp                 = "ssm:DescribeDocument"
	ssmDescribePatchBaselinesOp           = "ssm:DescribePatchBaselines"
	ssmListAssociationsOp                 = "ssm:ListAssociations"
	ssmDescribeMaintenanceWindowsOp       = "ssm:DescribeMaintenanceWindows"
	ssmDescribeMaintenanceWindowTargetsOp = "ssm:DescribeMaintenanceWindowTargets"
	ssmDescribeMaintenanceWindowTasksOp   = "ssm:DescribeMaintenanceWindowTasks"
	ssmListResourceDataSyncOp             = "ssm:ListResourceDataSync"
	ssmDescribeInstanceInformationOp      = "ssm:DescribeInstanceInformation"
	ssmListOpsMetadataOp                  = "ssm:ListOpsMetadata"
	ssmDescribeActivationsOp              = "ssm:DescribeActivations"
	ssmListCloudConnectorsOp              = "ssm:ListCloudConnectors"
	ssmTagsOp                             = "ssm:ListTagsForResource"

	// The page caps below are the maxima of the MaxResults ranges the model
	// gives each op.
	// ssmPageLimit: DescribeParameters, ListDocuments, ListAssociations,
	// ListResourceDataSync, DescribeActivations (MaxResults, 1..50),
	// ListOpsMetadata (ListOpsMetadataMaxResults, 1..50) and
	// DescribeInstanceInformation (MaxResultsEC2Compatible, 5..50).
	ssmPageLimit int32 = 50
	// ssmPatchBaselinePageLimit: DescribePatchBaselines
	// (PatchBaselineMaxResults, 1..100).
	ssmPatchBaselinePageLimit int32 = 100
	// ssmWindowPageLimit: DescribeMaintenanceWindows and its Targets and Tasks
	// (MaintenanceWindowMaxResults, 10..100).
	ssmWindowPageLimit int32 = 100
	// ssmCloudConnectorPageLimit: ListCloudConnectors
	// (CloudConnectorMaxResults, 0..10).
	ssmCloudConnectorPageLimit int32 = 10
)

// ssmTagsNotFound is the code ListTagsForResource models for an id it does
// not accept ("The resource ID isn't valid"), the only one it models for a
// resource it cannot find: a resource deleted after it was listed. That a
// deleted resource gets it is unverified live.
var ssmTagsNotFound = []string{"InvalidResourceId"}

// ssmTagIDFormDocumented holds the tag types whose id form AddTagsToResource
// documents. Only for these does ssmTagsOf read InvalidResourceId as a
// deleted resource. For the others (Association, CloudConnector) the code
// could equally mean that the id form sent is wrong, and dropping on it
// would silently erase every row of the type; there it is an error.
var ssmTagIDFormDocumented = map[types.ResourceTypeForTagging]bool{
	types.ResourceTypeForTaggingParameter:         true,
	types.ResourceTypeForTaggingPatchBaseline:     true,
	types.ResourceTypeForTaggingMaintenanceWindow: true,
	types.ResourceTypeForTaggingOpsmetadata:       true,
	types.ResourceTypeForTaggingManagedInstance:   true,
}

// ssmDocumentNotFound is DescribeDocument's modeled code for a document that
// does not exist: deleted after it was listed.
var ssmDocumentNotFound = []string{"InvalidDocument"}

// ssmWindowNotFound is the code DescribeMaintenanceWindowTargets and
// DescribeMaintenanceWindowTasks model for a window that does not exist:
// deleted after it was listed.
var ssmWindowNotFound = []string{"DoesNotExistException"}

// errSSMNoTagID fails the tag read of a row whose NativeID holds no id to
// send ListTagsForResource.
var errSSMNoTagID = errors.New("the resource's ARN holds no id to read its tags with")

// scanSSM discovers the SSM resources of one region.
func scanSSM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := ssm.NewFromConfig(acct.cfg, func(o *ssm.Options) { o.Region = region })
	return scanSSMAll(ctx, client, acct, region, st, scanID)
}

// scanSSMAll runs every SSM listing as its own phase, one at a time, in the
// order below (runHostPhases): a phase's error does not stop the later ones;
// a failed write and ctx's end do. The maintenance windows' targets and
// tasks are listed for the windows stored this scan, and, last, the sharing
// permission of every document stored this scan is read. A region whose
// first listing stores nothing and answers only NXDOMAIN is reported
// unavailable and nothing more is called.
//
// Parameters are read as metadata only, never their values: even with the
// store-level secret scrubber, plaintext would transit memory and risk
// landing in debug dumps. The KMS edge for SecureString parameters is
// recoverable from metadata alone.
//
// Documents and patch baselines are listed with Owner=Self, so no AWS-owned
// document or AWS-provided patch baseline is stored or has its tags read.
// Parameters, patch baselines, associations, maintenance windows,
// on-premises managed nodes (mi-; EC2 nodes' tags are their aws:ec2:instance
// row's), OpsMetadata objects and cloud connectors have their tags read per
// row (ssmTagsOf), each such listing written a page at a time, and the tag
// denials of the whole scan are one warning. Documents and activations take
// their tags from the listed element. Maintenance-window targets and tasks
// take none: the Service Reference names neither as a resource
// ListTagsForResource reads, and their Register ops take no Tags.
func scanSSMAll(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := rowTagReader{op: ssmTagsOp}.sharingDenials()
	var docs, windows []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "ssm", 0, []hostPhase{
		{op: ssmDescribeParametersOp, run: func(p *phaseTotals) bool {
			return scanSSMParameters(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: ssmListDocumentsOp, run: func(p *phaseTotals) (failed bool) {
			docs, failed = scanSSMDocuments(ctx, client, acct, region, st, scanID, p)
			return failed
		}},
		{op: ssmDescribePatchBaselinesOp, run: func(p *phaseTotals) bool {
			return scanSSMPatchBaselines(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: ssmListAssociationsOp, run: func(p *phaseTotals) bool {
			return scanSSMAssociations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: ssmDescribeMaintenanceWindowsOp, run: func(p *phaseTotals) (failed bool) {
			windows, failed = scanSSMMaintenanceWindows(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, ssmDescribeMaintenanceWindowTargetsOp, func() (int, int, error) {
			return scanSSMMaintenanceWindowTargets(ctx, client, acct, region, st, scanID, windows)
		}),
		countedPhase(0, ssmDescribeMaintenanceWindowTasksOp, func() (int, int, error) {
			return scanSSMMaintenanceWindowTasks(ctx, client, acct, region, st, scanID, windows)
		}),
		countedPhase(0, ssmListResourceDataSyncOp, func() (int, int, error) {
			return scanSSMResourceDataSync(ctx, client, acct, region, st, scanID)
		}),
		{op: ssmDescribeInstanceInformationOp, run: func(p *phaseTotals) bool {
			return scanSSMManagedInstances(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: ssmListOpsMetadataOp, run: func(p *phaseTotals) bool {
			return scanSSMOpsMetadata(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, ssmDescribeActivationsOp, func() (int, int, error) {
			return scanSSMActivations(ctx, client, acct, region, st, scanID)
		}),
		{op: ssmListCloudConnectorsOp, run: func(p *phaseTotals) bool {
			return scanSSMCloudConnectors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, ssmDescribeDocumentPermissionOp, func() (int, int, error) {
			return scanSSMDocumentPermissions(ctx, client, acct, region, st, scanID, docs)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// ssmTagsOf is tags reading each row's tags with ListTagsForResource as a
// resource of typ. The op takes the resource's id, not its ARN; id returns it
// for a row. An InvalidResourceId reply drops the row as deleted only when
// ssmTagIDFormDocumented holds typ. An empty id fails the read with
// errSSMNoTagID rather than being sent: the op would answer
// InvalidResourceId, read as a deletion for those types. The op does not
// page.
func ssmTagsOf(client ssmAPI, tags rowTagReader, typ types.ResourceTypeForTagging, id func(*store.Resource) string) rowTagReader {
	if ssmTagIDFormDocumented[typ] {
		tags.notFound = ssmTagsNotFound
	}
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		rid := id(r)
		if rid == "" {
			return nil, errSSMNoTagID
		}
		out, err := client.ListTagsForResource(ctx, &ssm.ListTagsForResourceInput{ResourceType: typ, ResourceId: &rid})
		if err != nil {
			return nil, err
		}
		return kvTagsJSON(out.TagList, ssmTagKV), nil
	}
	return tags
}

func ssmTagKV(t types.Tag) (key, value *string) { return t.Key, t.Value }

// ssmTagID returns the id a row's NativeID, an SSM ARN of resource type kind
// (arn:…:<kind>/<id>), ends with; "" when it has none.
func ssmTagID(kind string) func(*store.Resource) string {
	return func(r *store.Resource) string {
		_, id, _ := strings.Cut(r.NativeID, ":"+kind+"/")
		return id
	}
}

// scanSSMParameters stores the account's own parameters (DescribeParameters
// lists shared ones only when asked to), their tags read by name.
func scanSSMParameters(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, ssmDescribeParametersOp,
		func(ctx context.Context, token *string) ([]types.ParameterMetadata, *string, error) {
			out, err := client.DescribeParameters(ctx, &ssm.DescribeParametersInput{MaxResults: sdkaws.Int32(ssmPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Parameters, out.NextToken, nil
		},
		func(pm types.ParameterMetadata) (*store.Resource, string, string) {
			name := sv(pm.Name)
			if name == "" {
				return nil, "parameters have no name", cmp.Or(sv(pm.ARN), noIdentifier)
			}
			ptype := string(pm.Type)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMParameter, NativeID: ssmParameterARN(acct.partition(), region, acct.ID, name),
				Name: pm.Name, Region: &region, Status: &ptype,
				AttributesJSON: mustJSON(pm), DiscoveredBy: scanID,
			}, "", ""
		},
		ssmTagsOf(client, tags, types.ResourceTypeForTaggingParameter, func(r *store.Resource) string { return sv(r.Name) }), p)
}

// scanSSMDocuments stores the documents the account owns (Owner=Self: the
// AWS-owned ones number tens of thousands), each row's attributes replaced
// by its DescribeDocument body, which carries what the list element lacks
// (Requires, the cross-document references). A document DescribeDocument no
// longer finds is dropped; any other failure keeps the list element as the
// row (readRowDetails). Tags come from the list element. It returns the
// documents stored, keyed by name, as the parents of the permission phase,
// or storeWriteFailed (and none).
func scanSSMDocuments(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (docs []childParent, storeWriteFailed bool) {
	var hard, transient error
	total, inserted, docs, err := storeParentListWith(ctx, st, acct, region, ssmListDocumentsOp, "documents", "Name",
		func(ctx context.Context, token *string) ([]types.DocumentIdentifier, *string, error) {
			out, err := client.ListDocuments(ctx, &ssm.ListDocumentsInput{
				Filters:    []types.DocumentKeyValuesFilter{{Key: sp("Owner"), Values: []string{"Self"}}},
				MaxResults: sdkaws.Int32(ssmPageLimit),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.DocumentIdentifiers, out.NextToken, nil
		},
		func(d types.DocumentIdentifier) (*store.Resource, string, string) {
			name := sv(d.Name)
			if name == "" {
				return nil, "documents have no name", cmp.Or(sv(d.DisplayName), noIdentifier)
			}
			dtype := string(d.DocumentType)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMDocument, NativeID: arnFor(acct.partition(), "ssm", region, acct.ID, "document/"+name),
				Name: d.Name, Region: &region, Status: &dtype, CreatedAt: tp(d.CreatedDate),
				AttributesJSON: mustJSON(d), TagsJSON: kvTagsJSON(d.Tags, ssmTagKV), DiscoveredBy: scanID,
			}, "", ""
		},
		func(d types.DocumentIdentifier) string { return sv(d.Name) },
		func(rows []*store.Resource) ([]*store.Resource, func()) {
			kept, warn, h, tr := readRowDetails(ctx, st, acct, region, ssmDescribeDocumentOp, "document(s)", ssmDocumentNotFound, rows,
				func(ctx context.Context, r *store.Resource) (any, error) {
					out, err := client.DescribeDocument(ctx, &ssm.DescribeDocumentInput{Name: r.Name})
					if err != nil || out.Document == nil {
						return nil, err
					}
					return out.Document, nil
				})
			hard, transient = h, tr
			return kept, warn
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return nil, p.addPhase(ssmListDocumentsOp, total, inserted, errors.Join(err, hard, transient))
	}
	p.errs.add(ssmDescribeDocumentOp, hard)
	p.errs.add(ssmDescribeDocumentOp, transient)
	return docs, p.addPhase(ssmListDocumentsOp, total, inserted, err)
}

// scanSSMPatchBaselines stores the patch baselines the account owns
// (OWNER=Self), their tags read by baseline id.
func scanSSMPatchBaselines(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, ssmDescribePatchBaselinesOp,
		func(ctx context.Context, token *string) ([]types.PatchBaselineIdentity, *string, error) {
			out, err := client.DescribePatchBaselines(ctx, &ssm.DescribePatchBaselinesInput{
				Filters:    []types.PatchOrchestratorFilter{{Key: sp("OWNER"), Values: []string{"Self"}}},
				MaxResults: sdkaws.Int32(ssmPatchBaselinePageLimit),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.BaselineIdentities, out.NextToken, nil
		},
		func(b types.PatchBaselineIdentity) (*store.Resource, string, string) {
			id := sv(b.BaselineId)
			if id == "" {
				return nil, "patch baselines have no id", cmp.Or(sv(b.BaselineName), noIdentifier)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMPatchBaseline, NativeID: arnFor(acct.partition(), "ssm", region, acct.ID, "patchbaseline/"+id),
				Name: b.BaselineName, Region: &region,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			}, "", ""
		},
		ssmTagsOf(client, tags, types.ResourceTypeForTaggingPatchBaseline, ssmTagID("patchbaseline")), p)
}
