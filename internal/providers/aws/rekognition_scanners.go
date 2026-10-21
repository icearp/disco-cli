package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rekognitiontypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
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

// scanRekognition discovers Rekognition collections, custom-labels projects
// (with their versions, datasets and policies), and stream processors. Collections return plain string IDs (synth ARN);
// projects expose ProjectArn natively; stream processors return only
// (Name, Status) — synth ARN.
func scanRekognition(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := rekognition.NewFromConfig(acct.cfg, func(o *rekognition.Options) { o.Region = region })
	return scanRekognitionAll(ctx, client, acct, region, st, scanID)
}

// scanRekognitionAll runs the older phases, which stop at their first hard
// error, then the project-policy phase over the projects stored before that
// error, so a failure on either side costs the other no rows. The older
// side's transient error is demoted to a warning: one transient member would
// make the dispatcher read the whole join as transient, reporting the policy
// phase's hard error as a transient one.
func scanRekognitionAll(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, projectARNs, err := scanRekOlderPhases(ctx, client, acct, region, st, scanID)
	t, i, perr := scanRekProjectPolicies(ctx, client, acct, region, st, scanID, projectARNs)
	return total + t, inserted + i, errors.Join(demoteTransient(st, "rekognition", acct, region, err), perr)
}

// scanRekOlderPhases runs the phases that predate project policies
// sequentially, stopping at the first hard error, and returns the ARNs of the
// projects stored before it.
func scanRekOlderPhases(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, projectARNs []string, err error) {
	t, i, err := scanRekCollections(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, nil, err
	}
	total += t
	inserted += i

	projectARNs, t, i, err = scanRekProjects(ctx, client, acct, region, st, scanID)
	total += t
	inserted += i
	if err != nil {
		return total, inserted, projectARNs, err
	}

	t, i, err = scanRekProjectVersions(ctx, client, acct, region, st, scanID, projectARNs)
	if err != nil {
		return total, inserted, projectARNs, err
	}
	total += t
	inserted += i

	t, i, err = scanRekStreamProcessors(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, projectARNs, err
	}
	total += t
	inserted += i
	return total, inserted, projectARNs, nil
}

func scanRekCollections(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := rekognition.NewListCollectionsPaginator(client, &rekognition.ListCollectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "rekognition:ListCollections", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("rekognition:ListCollections: %w", err)
		}
		for _, id := range out.CollectionIds {
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:rekognition:%s:%s:collection/%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionCollection, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(map[string]string{"CollectionId": id}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "rekognition collections")
}

// scanRekProjects upserts projects and the datasets embedded in each project's
// DescribeProjects response (Datasets []DatasetMetadata — no separate list op);
// returns the project ARNs for the project-version fan-out.
func scanRekProjects(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := rekognition.NewDescribeProjectsPaginator(client, &rekognition.DescribeProjectsInput{})
	var batch, datasetBatch []*store.Resource
	var projectARNs []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isClosedToNewCustomers(err) {
				return nil, 0, 0, nil
			}
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "rekognition:DescribeProjects", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("rekognition:DescribeProjects: %w", err)
		}
		for _, p := range out.ProjectDescriptions {
			arn := sv(p.ProjectArn)
			if arn == "" {
				continue
			}
			projectARNs = append(projectARNs, arn)
			label := arn
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionProject, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
			for _, ds := range p.Datasets {
				dsARN := sv(ds.DatasetArn)
				if dsARN == "" {
					continue
				}
				dsLabel := dsARN
				dsStatus := string(ds.Status)
				datasetBatch = append(datasetBatch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRekognitionDataset, NativeID: dsARN,
					Name: &dsLabel, Region: &region, Status: &dsStatus,
					AttributesJSON: mustJSON(rekDatasetAttrs{DatasetMetadata: ds, ProjectArn: arn}), DiscoveredBy: scanID,
				})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "rekognition projects")
	if err != nil {
		return nil, t, i, err
	}
	dt, di, derr := upsertBatch(st, datasetBatch, "rekognition datasets")
	return projectARNs, t + dt, i + di, derr
}

// scanRekProjectVersions fans out DescribeProjectVersions (requires ProjectArn)
// across projects from scanRekProjects.
func scanRekProjectVersions(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string, projectARNs []string) (int, int, error) {
	if len(projectARNs) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, projARN := range projectARNs {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			pager := rekognition.NewDescribeProjectVersionsPaginator(client, &rekognition.DescribeProjectVersionsInput{ProjectArn: &projARN})
			for pager.HasMorePages() {
				out, derr := pager.NextPage(gctx)
				if derr != nil {
					if isAccessDenied(derr) {
						return nil
					}
					return fmt.Errorf("rekognition:DescribeProjectVersions %s: %w", projARN, derr)
				}
				for _, pv := range out.ProjectVersionDescriptions {
					arn := sv(pv.ProjectVersionArn)
					if arn == "" {
						continue
					}
					label := arn
					status := string(pv.Status)
					r := &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeRekognitionProjectVersion, NativeID: arn,
						Name: &label, Region: &region, Status: &status,
						AttributesJSON: mustJSON(rekProjectVersionAttrs{ProjectVersionDescription: pv, ProjectArn: projARN}), DiscoveredBy: scanID,
					}
					mu.Lock()
					batch = append(batch, r)
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	return upsertBatch(st, batch, "rekognition project-versions")
}

func scanRekStreamProcessors(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := rekognition.NewListStreamProcessorsPaginator(client, &rekognition.ListStreamProcessorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isClosedToNewCustomers(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "rekognition:ListStreamProcessors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("rekognition:ListStreamProcessors: %w", err)
		}
		for _, sp := range out.StreamProcessors {
			name := sv(sp.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:rekognition:%s:%s:streamprocessor/%s", region, acct.ID, name)
			status := string(sp.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionStreamProcessor, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(sp), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "rekognition stream-processors")
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
func scanRekProjectPolicies(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string, projectARNs []string) (int, int, error) {
	parents := make([]childParent, len(projectARNs))
	for i, arn := range projectARNs {
		parents[i] = childParent{id: arn, arn: arn}
	}
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	return childFanOutSkipTransient(ctx, st, acct, region, rekProjectPoliciesOp, parents, isGone, byParentARN, func(ctx context.Context, p childParent) ([]*store.Resource, error) {
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
