package aws

import (
	"cmp"
	"context"
	"strconv"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
	ListTagsForResource(context.Context, *healthlake.ListTagsForResourceInput, ...func(*healthlake.Options)) (*healthlake.ListTagsForResourceOutput, error)
}

const (
	healthLakeListDatastoresOp = "health-lake:ListFHIRDatastores"
	healthLakeListProfilesOp   = "health-lake:ListDataTransformationProfiles"
	healthLakeListVersionsOp   = "health-lake:ListDataTransformationProfileVersions"
	healthLakeTagsOp           = "health-lake:ListTagsForResource"
	// healthLakeDatastorePageCap is ListFHIRDatastores' MaxResults maximum
	// (the model's range is 1-500).
	healthLakeDatastorePageCap = 500
	// healthLakeProfilePageCap is the MaxResults maximum of the profile and
	// profile version listings (the model's range is 1-100).
	healthLakeProfilePageCap = 100
)

// The hosts the scan calls, for runHostPhases: the profile ops carry the
// model's "datatransformation." host prefix, so a region can serve the
// datastore API without them. ListTagsForResource has no prefix and goes to
// the datastore host for every row.
const (
	healthLakeDatastoreHost = iota
	healthLakeDataTransformationHost
)

// healthLakeNotFound is the not-found code ListTagsForResource and
// ListDataTransformationProfileVersions model, for a resource deleted after
// it was listed. ListFHIRDatastores and ListDataTransformationProfiles model
// none.
var healthLakeNotFound = []string{"ResourceNotFoundException"}

// scanHealthLake discovers HealthLake FHIR datastores and data transformation
// profiles, each with its tags (healthLakeTags), and the profiles' versions.
// Not scanned: FHIR import/export and data transformation jobs (run records).
//
// The listings run as phases, one at a time (runHostPhases): a phase's error
// does not stop the later ones; a failed write and ctx's end do. The version
// phase lists the profiles the profile phase stored. A region whose
// data transformation host does not resolve still stores its datastores. The
// tag reads' denials are one warning for the whole scan.
func scanHealthLake(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := healthlake.NewFromConfig(acct.cfg, func(o *healthlake.Options) { o.Region = region })
	tags := healthLakeTags(client).sharingDenials()
	var profiles []childParent
	total, err := runHostPhases(ctx, st, acct, region, "health-lake", healthLakeDatastoreHost, []hostPhase{
		{op: healthLakeListDatastoresOp, host: healthLakeDatastoreHost, run: func(p *phaseTotals) bool {
			return scanHealthLakeDatastores(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: healthLakeListProfilesOp, host: healthLakeDataTransformationHost, run: func(p *phaseTotals) bool {
			var failed bool
			profiles, failed = scanHealthLakeProfiles(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(healthLakeDataTransformationHost, healthLakeListVersionsOp, func() (int, error) {
			return scanHealthLakeProfileVersions(ctx, client, acct, region, st, scanID, profiles)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// healthLakeTags reads a datastore's or a profile's tags with
// ListTagsForResource, sent the row's NativeID: the DatastoreArn the listing
// returned, or the profile ARN healthLakeProfileARN builds. The Service
// Reference lists both datastore and dataTransformationProfile on
// ListTagsForResource and TagResource with aws:ResourceTag, the op's ARN
// pattern accepts both, and CreateFHIRDatastore and
// CreateDataTransformationProfile take Tags; no list element carries them.
// ResourceNotFoundException, the op's only modeled not-found, drops a
// resource deleted after it was listed.
//
// A DELETED datastore is not read and stays untagged: ListFHIRDatastores
// returns datastores in every status, and a read of one that no longer
// exists could answer ResourceNotFoundException, which would drop the listed
// row. Only datastore rows carry a Status.
func healthLakeTags(client healthLakeAPI) rowTagReader {
	return rowTagReader{
		op:       healthLakeTagsOp,
		notFound: healthLakeNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if sv(r.Status) == string(healthlaketypes.DatastoreStatusDeleted) {
				return nil, nil
			}
			out, err := client.ListTagsForResource(ctx, &healthlake.ListTagsForResourceInput{ResourceARN: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t healthlaketypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanHealthLakeDatastores stores the datastores with their tags, a page at a
// time (storeTokenPagesTags). Counts and errors go to p; it reports whether
// a write failed, after which the scan must stop.
func scanHealthLakeDatastores(ctx context.Context, client healthLakeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, healthLakeListDatastoresOp,
		func(ctx context.Context, token *string) ([]healthlaketypes.DatastoreProperties, *string, error) {
			out, err := client.ListFHIRDatastores(ctx, &healthlake.ListFHIRDatastoresInput{MaxResults: sdkaws.Int32(healthLakeDatastorePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DatastorePropertiesList, out.NextToken, nil
		},
		func(d healthlaketypes.DatastoreProperties) (*store.Resource, string, string) {
			return arnRow("FHIR datastores", d.DatastoreArn, d.DatastoreName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeHealthLakeFHIRDatastore, NativeID: sv(d.DatastoreArn),
				Name: d.DatastoreName, Region: &region, Status: nonEmptyPtr(string(d.DatastoreStatus)),
				CreatedAt: tp(d.CreatedAt), AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// healthLakeProfileARN builds the profile ARN the Service Authorization
// Reference documents; the list API returns only the profile id.
func healthLakeProfileARN(partition, region, accountID, profileID string) string {
	return arnFor(partition, "healthlake", region, accountID, "dataTransformationProfile/"+profileID)
}

// scanHealthLakeProfiles stores the data transformation profiles with their
// tags and returns those stored as the version phase's parents. The listing
// requires a SourceFormat filter and returns only that format's profiles, so
// it is made once per format the SDK knows, the formats read concurrently
// and each page written as read (storeNestedParentPagesTags): one format's
// failure keeps the other format's rows, and a denial warns once. Profile
// content (GetDataTransformationProfile) is not fetched. Counts and errors go
// to p; it reports whether a write failed, after which the scan must stop.
func scanHealthLakeProfiles(ctx context.Context, client healthLakeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	var formats []childParent
	for _, f := range healthlaketypes.SourceFormat("").Values() {
		formats = append(formats, childParent{id: string(f)})
	}
	return storeNestedParentPagesTags(ctx, st, acct, region, healthLakeListProfilesOp, "data transformation profiles", "ProfileId", nil, formats,
		func(ctx context.Context, format string, token *string) ([]healthlaketypes.DataTransformationProfileSummary, *string, error) {
			out, err := client.ListDataTransformationProfiles(ctx, &healthlake.ListDataTransformationProfilesInput{
				SourceFormat: healthlaketypes.SourceFormat(format), MaxResults: sdkaws.Int32(healthLakeProfilePageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(_ string, pr healthlaketypes.DataTransformationProfileSummary) (*store.Resource, string, string) {
			id := sv(pr.ProfileId)
			if id == "" {
				return nil, "data transformation profiles have no ProfileId", cmp.Or(sv(pr.ProfileName), noIdentifier)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeHealthLakeDataTransformationProfile, NativeID: healthLakeProfileARN(acct.partition(), region, acct.ID, id),
				Name: pr.ProfileName, Region: &region,
				AttributesJSON: mustJSON(pr), DiscoveredBy: scanID,
			}, "", ""
		},
		func(pr healthlaketypes.DataTransformationProfileSummary) string { return sv(pr.ProfileId) },
		tags, p)
}

// scanHealthLakeProfileVersions lists the versions of each given profile:
// the DRAFT (version 0) and every published version, which is immutable and
// lives until its profile is deleted. Version numbers are capped at 99, so a
// profile has at most 100. The profiles are read concurrently and each is
// classified on its own (storeChildrenEachParent): a profile deleted since it
// was listed is skipped, a denial warns once, and any other error is
// returned after the other profiles' versions are stored. Each version is
// recorded under its profile.
//
// Versions have no tags read: the Service Reference defines no version
// resource, ListTagsForResource's ARN pattern accepts only datastore and
// profile ARNs, and no version response carries tags.
func scanHealthLakeProfileVersions(ctx context.Context, client healthLakeAPI, acct *account, region string, st *store.Store, scanID string, profiles []childParent) (int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, healthLakeListVersionsOp, healthLakeNotFound, profiles, nil, nil,
		func(ctx context.Context, p childParent, token *string) ([]healthlaketypes.DataTransformationProfileVersionSummary, *string, error) {
			out, err := client.ListDataTransformationProfileVersions(ctx, &healthlake.ListDataTransformationProfileVersionsInput{
				ProfileId: &p.id, MaxResults: sdkaws.Int32(healthLakeProfilePageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(p childParent, v healthlaketypes.DataTransformationProfileVersionSummary) (*store.Resource, string, string) {
			if v.Version == nil {
				return nil, "data transformation profile versions have no Version", p.arn
			}
			version := strconv.Itoa(int(*v.Version))
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeHealthLakeDataTransformationProfileVersion, NativeID: p.arn + "/version/" + version,
				Name: &version, Region: &region,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			}, "", ""
		})
}
