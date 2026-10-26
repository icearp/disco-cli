package aws

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/service/healthlake"
	healthlaketypes "github.com/aws/aws-sdk-go-v2/service/healthlake/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeHealthLakeFHIRDatastore, Service: "health-lake"})
	registerType(restype.Descriptor{Type: TypeHealthLakeDataTransformationProfile, Service: "health-lake"})
	registerType(restype.Descriptor{Type: TypeHealthLakeDataTransformationProfileVersion, Service: "health-lake"})
	registerService(serviceEntry{
		name: "aws:health-lake",
		fn:   scanHealthLake,
	})
}

type healthLakeAPI interface {
	ListFHIRDatastores(context.Context, *healthlake.ListFHIRDatastoresInput, ...func(*healthlake.Options)) (*healthlake.ListFHIRDatastoresOutput, error)
	ListDataTransformationProfiles(context.Context, *healthlake.ListDataTransformationProfilesInput, ...func(*healthlake.Options)) (*healthlake.ListDataTransformationProfilesOutput, error)
	ListDataTransformationProfileVersions(context.Context, *healthlake.ListDataTransformationProfileVersionsInput, ...func(*healthlake.Options)) (*healthlake.ListDataTransformationProfileVersionsOutput, error)
}

// scanHealthLake discovers HealthLake FHIR datastores and data transformation
// profiles with their versions. Not scanned: FHIR import/export and data
// transformation jobs (run records).
//
// The profile phases run after the datastore phase, so their failure cannot
// cost the datastore rows.
func scanHealthLake(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := healthlake.NewFromConfig(acct.cfg, func(o *healthlake.Options) { o.Region = region })

	var profiles []childParent
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanHealthLakeDatastores(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			profiles, t, i, err = scanHealthLakeProfiles(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) {
			return scanHealthLakeProfileVersions(ctx, client, acct, region, st, scanID, profiles)
		},
	} {
		t, i, ferr := phase()
		total += t
		inserted += i
		if ferr != nil {
			return total, inserted, ferr
		}
	}
	return total, inserted, nil
}

func scanHealthLakeDatastores(ctx context.Context, client healthLakeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListFHIRDatastores(ctx, &healthlake.ListFHIRDatastoresInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "health-lake:ListFHIRDatastores", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("health-lake:ListFHIRDatastores: %w", err)
		}
		for _, d := range out.DatastorePropertiesList {
			arn := sv(d.DatastoreArn)
			if arn == "" {
				continue
			}
			status := string(d.DatastoreStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeHealthLakeFHIRDatastore, NativeID: arn,
				Name: d.DatastoreName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "health-lake fhir-datastores")
}

// healthLakeProfileARN builds the profile ARN the Service Authorization
// Reference documents; the list API returns only the profile id.
func healthLakeProfileARN(partition, region, accountID, profileID string) string {
	return arnFor(partition, "healthlake", region, accountID, "dataTransformationProfile/"+profileID)
}

// scanHealthLakeProfiles stores the profiles listHealthLakeProfiles lists and
// returns them as version fan-out parents. Profile content
// (GetDataTransformationProfile) is not fetched. Rows listed before a failure
// are stored and counted.
func scanHealthLakeProfiles(ctx context.Context, client healthLakeAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	const op = "health-lake:ListDataTransformationProfiles"
	batch, parents, listErr := listHealthLakeProfiles(ctx, client, acct, region, scanID)
	total, inserted, err := upsertBatch(st, batch, "health-lake data-transformation-profiles")
	if err != nil {
		if listErr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", op, listErr))
		}
		return nil, 0, 0, err
	}
	switch {
	case listErr == nil:
	case isAccessDenied(listErr):
		_ = skipIfAccessDenied(st, op, acct.ID, region, listErr)
	default:
		return nil, total, inserted, fmt.Errorf("%s: %w", op, listErr)
	}
	return parents, total, inserted, nil
}

// listHealthLakeProfiles lists the data transformation profiles of every
// source format the SDK knows: the API requires a SourceFormat filter and
// returns only that format's profiles. On error it also returns the rows
// listed before it.
func listHealthLakeProfiles(ctx context.Context, client healthLakeAPI, acct *account, region, scanID string) ([]*store.Resource, []childParent, error) {
	var (
		batch   []*store.Resource
		parents []childParent
	)
	for _, format := range healthlaketypes.SourceFormat("").Values() {
		pager := healthlake.NewListDataTransformationProfilesPaginator(client, &healthlake.ListDataTransformationProfilesInput{SourceFormat: format})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return batch, parents, err
			}
			for _, p := range out.Items {
				id := sv(p.ProfileId)
				if id == "" {
					continue
				}
				arn := healthLakeProfileARN(acct.partition(), region, acct.ID, id)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeHealthLakeDataTransformationProfile, NativeID: arn,
					Name: p.ProfileName, Region: &region,
					AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
				parents = append(parents, childParent{id: id, arn: arn})
			}
		}
	}
	return batch, parents, nil
}

// scanHealthLakeProfileVersions lists the versions of each given profile:
// the DRAFT (version 0) and every published version, which is immutable and
// lives until its profile is deleted. Version numbers are capped at 99, so a
// profile has at most 100.
func scanHealthLakeProfileVersions(ctx context.Context, client healthLakeAPI, acct *account, region string, st *store.Store, scanID string, profiles []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "health-lake:ListDataTransformationProfileVersions", profiles, isHealthLakeNotFound,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := healthlake.NewListDataTransformationProfileVersionsPaginator(client, &healthlake.ListDataTransformationProfileVersionsInput{ProfileId: &p.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, v := range out.Items {
					if v.Version == nil {
						continue
					}
					version := strconv.Itoa(int(*v.Version))
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeHealthLakeDataTransformationProfileVersion, NativeID: p.arn + "/version/" + version,
						Name: &version, Region: &region,
						AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// isHealthLakeNotFound matches a profile deleted between the phase that
// listed it and the call listing its versions.
func isHealthLakeNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}
