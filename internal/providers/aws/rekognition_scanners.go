package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
)

func init() {
	registerService(serviceEntry{
		name: "aws:rekognition",
		fn:   scanRekognition,
		emits: []coverage.TypeDecl{
			{Service: "rekognition", DiscoType: TypeRekognitionCollection, Leaf: true},
			{Service: "rekognition", DiscoType: TypeRekognitionProject, Leaf: true},
			{Service: "rekognition", DiscoType: TypeRekognitionStreamProcessor, Leaf: true},
		},
	})
}

type rekognitionAPI interface {
	ListCollections(context.Context, *rekognition.ListCollectionsInput, ...func(*rekognition.Options)) (*rekognition.ListCollectionsOutput, error)
	DescribeProjects(context.Context, *rekognition.DescribeProjectsInput, ...func(*rekognition.Options)) (*rekognition.DescribeProjectsOutput, error)
	ListStreamProcessors(context.Context, *rekognition.ListStreamProcessorsInput, ...func(*rekognition.Options)) (*rekognition.ListStreamProcessorsOutput, error)
}

// scanRekognition discovers Rekognition collections, custom-labels projects,
// and stream processors. Collections come back as plain string IDs (synth
// ARN); projects expose ProjectArn natively; stream processors return only
// (Name, Status) — synth ARN.
func scanRekognition(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := rekognition.NewFromConfig(acct.cfg, func(o *rekognition.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanRekCollections(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRekProjects(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRekStreamProcessors(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
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

func scanRekProjects(ctx context.Context, client rekognitionAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := rekognition.NewDescribeProjectsPaginator(client, &rekognition.DescribeProjectsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isClosedToNewCustomers(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "rekognition:DescribeProjects", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("rekognition:DescribeProjects: %w", err)
		}
		for _, p := range out.ProjectDescriptions {
			arn := sv(p.ProjectArn)
			if arn == "" {
				continue
			}
			label := arn
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRekognitionProject, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "rekognition projects")
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
