package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rekognitiontypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRekognitionCollection, Service: "rekognition"})
	registerType(restype.Descriptor{Type: TypeRekognitionProject, Service: "rekognition"})
	registerType(restype.Descriptor{Type: TypeRekognitionStreamProcessor, Service: "rekognition"})
	registerType(restype.Descriptor{Type: TypeRekognitionProjectVersion, Service: "rekognition"})
	registerType(restype.Descriptor{Type: TypeRekognitionDataset, Service: "rekognition"})
	registerType(restype.Descriptor{Type: TypeRekognitionProjectPolicy, Service: "rekognition"})
	registerService(serviceEntry{
		name: "aws:rekognition",
		fn:   scanRekognition,
	})
}

type rekognitionAPI interface {
	ListCollections(context.Context, *rekognition.ListCollectionsInput, ...func(*rekognition.Options)) (*rekognition.ListCollectionsOutput, error)
	DescribeProjects(context.Context, *rekognition.DescribeProjectsInput, ...func(*rekognition.Options)) (*rekognition.DescribeProjectsOutput, error)
	ListStreamProcessors(context.Context, *rekognition.ListStreamProcessorsInput, ...func(*rekognition.Options)) (*rekognition.ListStreamProcessorsOutput, error)
	DescribeProjectVersions(context.Context, *rekognition.DescribeProjectVersionsInput, ...func(*rekognition.Options)) (*rekognition.DescribeProjectVersionsOutput, error)
	ListProjectPolicies(context.Context, *rekognition.ListProjectPoliciesInput, ...func(*rekognition.Options)) (*rekognition.ListProjectPoliciesOutput, error)
	ListTagsForResource(context.Context, *rekognition.ListTagsForResourceInput, ...func(*rekognition.Options)) (*rekognition.ListTagsForResourceOutput, error)
}

// rekDatasetAttrs embeds SDK DatasetMetadata plus the parent ProjectArn, giving
// the dataset→project resolver an FK-safe target (DatasetArn alone doesn't
// encode the parent project ARN).
type rekDatasetAttrs struct {
	rekognitiontypes.DatasetMetadata
	ProjectArn string `json:"ProjectArn"`
}

// rekProjectVersionAttrs embeds SDK ProjectVersionDescription plus the parent
// ProjectArn (the description exposes only ProjectVersionArn).
type rekProjectVersionAttrs struct {
	rekognitiontypes.ProjectVersionDescription
	ProjectArn string `json:"ProjectArn"`
}

const (
	rekTagsOp                    = "rekognition:ListTagsForResource"
	rekListCollectionsOp         = "rekognition:ListCollections"
	rekDescribeProjectsOp        = "rekognition:DescribeProjects"
	rekDescribeProjectVersionsOp = "rekognition:DescribeProjectVersions"
	rekListStreamProcessorsOp    = "rekognition:ListStreamProcessors"
	// rekCollectionPageCap is the model's maximum ListCollections MaxResults.
	rekCollectionPageCap = 4096
	// rekProjectPageCap is the model's maximum DescribeProjects and
	// DescribeProjectVersions MaxResults; a larger value is rejected with a
	// ValidationException.
	rekProjectPageCap = 100
)

// rekGone is the not-found code ListTagsForResource models, and the one
// DescribeProjectVersions models for a project deleted since it was listed.
var rekGone = []string{"ResourceNotFoundException"}

// scanRekognition discovers Rekognition collections, Custom Labels projects
// with the datasets each embeds, their versions and policies, and stream
// processors. Every listing runs as its own phase, one at a time: a phase's
// error does not stop the later ones; a failed write and ctx's end do
// (runHostPhases). The per-project phases read the projects stored this scan.
// Every type but project policies (no ARN; the Service Reference lists no
// policy resource) has its tags read per row (rekTags), whose denials are one
// warning for the scan.
func scanRekognition(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := rekognition.NewFromConfig(acct.cfg, func(o *rekognition.Options) { o.Region = region })
	tags := rekTags(client).sharingDenials()
	var projects []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "rekognition", 0, []hostPhase{
		{op: rekListCollectionsOp, run: func(p *phaseTotals) bool {
			return scanRekCollections(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: rekDescribeProjectsOp, run: func(p *phaseTotals) (failed bool) {
			projects, failed = scanRekProjects(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: rekDescribeProjectVersionsOp, run: func(p *phaseTotals) bool {
			return scanRekProjectVersions(ctx, client, acct, region, st, scanID, projects, tags, p)
		}},
		{op: rekListStreamProcessorsOp, run: func(p *phaseTotals) bool {
			return scanRekStreamProcessors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, rekProjectPoliciesOp, func() (int, int, error) {
			return scanRekProjectPolicies(ctx, client, acct, region, st, scanID, projects)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// rekTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID. For projects, datasets and project versions that is the ARN their
// listing returned. Collections and stream processors are listed without one,
// so it is the ARN built, not read, from the listed id or name in the Service
// Reference's format (collection/${CollectionId},
// streamprocessor/${StreamprocessorId}). A stream processor's ARN is returned
// only by DescribeStreamProcessor and CreateStreamProcessor; building it from
// the listed Name saves a Describe call per processor, on the assumption,
// unverified live, that the ARN's StreamprocessorId is the Name. The Service
// Reference lists all five types under ListTagsForResource, and each type's
// Create op takes Tags. The op does not page. ResourceNotFoundException, its
// modeled not-found, drops a resource deleted after it was listed.
func rekTags(client rekognitionAPI) rowTagReader {
	return rowTagReader{
		op:       rekTagsOp,
		notFound: rekGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &rekognition.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// rekCollection is a listed collection, and its stored attributes.
type rekCollection struct {
	CollectionID     string `json:"CollectionId"`
	FaceModelVersion string `json:"FaceModelVersion,omitempty"`
}

// rekCollections pairs ListCollections' CollectionIds with FaceModelVersions,
// which the model documents as index-aligned with them. A collection past the
// end of a shorter FaceModelVersions is kept without a version.
func rekCollections(out *rekognition.ListCollectionsOutput) []rekCollection {
	cols := make([]rekCollection, len(out.CollectionIds))
	for i, id := range out.CollectionIds {
		cols[i].CollectionID = id
		if i < len(out.FaceModelVersions) {
			cols[i].FaceModelVersion = out.FaceModelVersions[i]
		}
	}
	return cols
}

// scanRekCollections stores the collections with their tags, a page at a
// time. ListCollections returns no ARN; each collection is stored under the
// ARN built from its id.
func scanRekCollections(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, rekListCollectionsOp,
		func(ctx context.Context, token *string) ([]rekCollection, *string, error) {
			out, err := client.ListCollections(ctx, &rekognition.ListCollectionsInput{MaxResults: sdkaws.Int32(rekCollectionPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return rekCollections(out), out.NextToken, nil
		},
		func(c rekCollection) (*store.Resource, string, string) {
			if c.CollectionID == "" {
				return nil, "collections have no id", noIdentifier
			}
			label := c.CollectionID
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionCollection, NativeID: arnFor(acct.partition(), "rekognition", region, acct.ID, "collection/"+c.CollectionID),
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}

// scanRekProjects stores the projects with their tags, a page at a time, then
// the datasets each stored project's element embeds (scanRekDatasets). It
// returns the stored projects, the parents of the per-project phases, and
// whether a write failed, after which the scan must stop. Without a Features
// filter DescribeProjects lists Custom Labels projects only. An account the
// service is closed to (isClosedToNewCustomers) on the first page has none.
func scanRekProjects(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	datasets := map[string][]rekognitiontypes.DatasetMetadata{}
	projects, failed := storeParentPagesTags(ctx, st, acct, region, rekDescribeProjectsOp, "projects", "ProjectArn",
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]rekognitiontypes.ProjectDescription, *string, error) {
			out, err := client.DescribeProjects(ctx, &rekognition.DescribeProjectsInput{MaxResults: sdkaws.Int32(rekProjectPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ProjectDescriptions, out.NextToken, nil
		}),
		func(pd rekognitiontypes.ProjectDescription) (*store.Resource, string, string) {
			arn := sv(pd.ProjectArn)
			status := string(pd.Status)
			datasets[arn] = pd.Datasets
			return arnRow("projects", pd.ProjectArn, nil, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionProject, NativeID: arn,
				Name: &arn, Region: &region, Status: &status, CreatedAt: tp(pd.CreationTimestamp),
				AttributesJSON: mustJSON(pd), DiscoveredBy: scanID,
			})
		},
		func(pd rekognitiontypes.ProjectDescription) string { return sv(pd.ProjectArn) },
		tags, p)
	if failed {
		return nil, true
	}
	return projects, scanRekDatasets(ctx, st, acct, region, scanID, projects, datasets, tags, p)
}

// scanRekDatasets stores, with their tags, the datasets the DescribeProjects
// element of each stored project embeds (Datasets): there is no dataset
// listing. A project holds at most a train and a test dataset (the model's
// DatasetArn pattern), so they are held until one write. It reports whether
// the write failed.
func scanRekDatasets(ctx context.Context, st *store.Store, acct *account, region, scanID string, projects []childParent, datasets map[string][]rekognitiontypes.DatasetMetadata, tags rowTagReader, p *phaseTotals) bool {
	var (
		rows  []*store.Resource
		skips notStoredSkips
	)
	for _, project := range projects {
		rows = buildListedRows(datasets[project.arn], func(ds rekognitiontypes.DatasetMetadata) (*store.Resource, string, string) {
			arn := sv(ds.DatasetArn)
			status := string(ds.Status)
			return arnRow("datasets", ds.DatasetArn, &project.arn, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionDataset, NativeID: arn,
				Name: &arn, Region: &region, Status: &status, CreatedAt: tp(ds.CreationTimestamp),
				AttributesJSON: mustJSON(rekDatasetAttrs{DatasetMetadata: ds, ProjectArn: project.arn}), DiscoveredBy: scanID,
			})
		}, rows, &skips)
	}
	return storeRowsTags(ctx, st, acct, region, rekDescribeProjectsOp, rows, nil, func() { skips.warn(st, rekDescribeProjectsOp, acct, region) }, tags, p)
}

// scanRekProjectVersions stores each project's versions with their tags. A
// project gains a version per training, so each page is written as soon as
// its tags are read (storeEachParentPagesTags); one project's failure costs
// the others none of theirs.
func scanRekProjectVersions(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string, projects []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, rekDescribeProjectVersionsOp, rekGone, projects,
		func(ctx context.Context, projectARN string, token *string) ([]rekognitiontypes.ProjectVersionDescription, *string, error) {
			out, err := client.DescribeProjectVersions(ctx, &rekognition.DescribeProjectVersionsInput{ProjectArn: &projectARN, MaxResults: sdkaws.Int32(rekProjectPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ProjectVersionDescriptions, out.NextToken, nil
		},
		func(projectARN string, pv rekognitiontypes.ProjectVersionDescription) (*store.Resource, string, string) {
			arn := sv(pv.ProjectVersionArn)
			status := string(pv.Status)
			return arnRow("project versions", pv.ProjectVersionArn, &projectARN, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionProjectVersion, NativeID: arn,
				Name: &arn, Region: &region, Status: &status, CreatedAt: tp(pv.CreationTimestamp),
				AttributesJSON: mustJSON(rekProjectVersionAttrs{ProjectVersionDescription: pv, ProjectArn: projectARN}), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanRekStreamProcessors stores the stream processors with their tags, a
// page at a time. ListStreamProcessors returns no ARN; each processor is stored
// under the ARN built from its name (rekTags). The model sets no maximum
// MaxResults (its default is 1000), so none is sent. An account the service
// is closed to (isClosedToNewCustomers) on the first page has none.
func scanRekStreamProcessors(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, rekListStreamProcessorsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]rekognitiontypes.StreamProcessor, *string, error) {
			out, err := client.ListStreamProcessors(ctx, &rekognition.ListStreamProcessorsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.StreamProcessors, out.NextToken, nil
		}),
		func(sp rekognitiontypes.StreamProcessor) (*store.Resource, string, string) {
			name := sv(sp.Name)
			if name == "" {
				return nil, "stream processors have no name", noIdentifier
			}
			status := string(sp.Status)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionStreamProcessor, NativeID: arnFor(acct.partition(), "rekognition", region, acct.ID, "streamprocessor/"+name),
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(sp), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}

const rekProjectPoliciesOp = "rekognition:ListProjectPolicies"

// rekProjectPolicyPageLimit is the model's cap on ListProjectPolicies
// MaxResults; a larger value is rejected with a ValidationException.
const rekProjectPolicyPageLimit = 5

// rekProjectPolicyNativeID builds a project policy's identity: the API
// returns no ARN, and a policy name is unique within its project.
func rekProjectPolicyNativeID(projectARN, policyName string) string {
	return projectARN + "/policy/" + policyName
}

// scanRekProjectPolicies stores the resource-based policies of each project
// scanRekProjects stored, each under its project. ListProjectPolicies applies
// only to Custom Labels projects, the only kind DescribeProjects lists without
// a Features filter. Per project: ResourceNotFoundException (deleted since
// listing) is skipped silently, a denial warns once for the op, a transient
// error after the SDK's retries is skipped with one warning for all such
// projects, and anything else fails the phase, storing none of its rows.
func scanRekProjectPolicies(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string, projects []childParent) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	return childFanOutSkipTransient(ctx, st, acct, region, rekProjectPoliciesOp, projects, isGone, byParentARN, func(ctx context.Context, p childParent) ([]*store.Resource, error) {
		return listRekProjectPolicies(ctx, client, acct, region, scanID, p.arn)
	})
}

func listRekProjectPolicies(ctx context.Context, client rekognitionAPI, acct *account, region, scanID, projectARN string) ([]*store.Resource, error) {
	pager := rekognition.NewListProjectPoliciesPaginator(client,
		&rekognition.ListProjectPoliciesInput{ProjectArn: &projectARN},
		func(o *rekognition.ListProjectPoliciesPaginatorOptions) { o.Limit = rekProjectPolicyPageLimit })
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, pol := range out.ProjectPolicies {
			name := sv(pol.PolicyName)
			if name == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionProjectPolicy, NativeID: rekProjectPolicyNativeID(projectARN, name),
				Name: &name, Region: &region, CreatedAt: tp(pol.CreationTimestamp),
				AttributesJSON: mustJSON(pol), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
