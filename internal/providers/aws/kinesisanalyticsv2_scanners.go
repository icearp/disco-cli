package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2"
	kav2types "github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeKAV2Application, Service: "kinesis-analytics-v2"})
	registerType(restype.Descriptor{Type: TypeKAV2ApplicationCloudWatchLogOpt, Service: "kinesis-analytics-v2"})
	registerType(restype.Descriptor{Type: TypeKAV2ApplicationOutput, Service: "kinesis-analytics-v2"})
	registerType(restype.Descriptor{Type: TypeKAV2ApplicationReferenceData, Service: "kinesis-analytics-v2"})
	registerType(restype.Descriptor{Type: TypeKAV2ApplicationSnapshot, Service: "kinesis-analytics-v2"})
	registerService(serviceEntry{
		name: "aws:kinesis-analytics-v2",
		fn:   scanKinesisAnalyticsV2,
	})
}

const kav2ListApplicationSnapshotsOp = "kinesisanalyticsv2:ListApplicationSnapshots"

// kav2SnapshotPageLimit is the model's cap on ListApplicationSnapshots Limit
// (range 1..50).
const kav2SnapshotPageLimit = 50

// scanKinesisAnalyticsV2 discovers Kinesis Data Analytics v2 applications,
// their config sub-resources, and then the snapshots of every application it
// stored. The snapshot phase runs whatever the application phase returned, so
// its failure costs the applications nothing. The application phase's transient
// error is demoted to a warning only beside a hard error: joined, it would be
// buried in the hard error instead of reported under its op; the counts are
// summed.
func scanKinesisAnalyticsV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := kinesisanalyticsv2.NewFromConfig(acct.cfg, func(o *kinesisanalyticsv2.Options) { o.Region = region })
	total, inserted, apps, err := scanKAV2Applications(ctx, client, acct, region, st, scanID)
	t, i, serr := scanKAV2ApplicationSnapshots(ctx, client, acct, region, st, scanID, apps)
	return total + t, inserted + i, demoteBeside(st, "kinesisanalyticsv2", acct, region, err, serr)
}

// scanKAV2Applications discovers the applications and their config
// sub-resources (CloudWatch logging options, outputs, reference data sources),
// embedded in each app's DescribeApplication response. It returns the
// applications stored before any failure, keyed by name.
func scanKAV2Applications(ctx context.Context, client *kinesisanalyticsv2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, apps []childParent, err error) {
	pager := kinesisanalyticsv2.NewListApplicationsPaginator(client, &kinesisanalyticsv2.ListApplicationsInput{})
	// Each application is stored once per scan. The store has no in-place
	// update: a second upsert of the same ARN with different attributes is a
	// version split, so writing the summary and then the detail body added two
	// versions per application on every scan. The summary is kept only as the
	// fallback row when the detail read is denied or empty.
	var summaries []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, nil, skipIfAccessDenied(st, "kinesisanalyticsv2:ListApplications", acct.ID, region, perr)
			}
			return 0, 0, nil, fmt.Errorf("kinesisanalyticsv2:ListApplications: %w", perr)
		}
		for _, s := range out.ApplicationSummaries {
			arn := sv(s.ApplicationARN)
			name := sv(s.ApplicationName)
			if arn == "" || name == "" {
				continue
			}
			status := string(s.ApplicationStatus)
			summaries = append(summaries, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKAV2Application, NativeID: arn,
				Name: s.ApplicationName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}

	// A failed detail read still stores the application from its summary and
	// moves on; the failure is returned once every application is stored. An
	// application deleted since listing is dropped silently: no row, and no
	// snapshot listing.
	var describeErrs []error
	for _, summary := range summaries {
		nm := *summary.Name
		out, derr := client.DescribeApplication(ctx, &kinesisanalyticsv2.DescribeApplicationInput{ApplicationName: &nm})
		if isKAV2AppGone(derr) {
			continue
		}
		if derr != nil && !isAccessDenied(derr) {
			describeErrs = append(describeErrs, fmt.Errorf("kinesisanalyticsv2:DescribeApplication %s: %w", nm, derr))
		}
		var d *kav2types.ApplicationDetail
		if derr == nil {
			d = out.ApplicationDetail
		}
		if d == nil || sv(d.ApplicationARN) == "" {
			t, i, ferr := upsertBatch(st, []*store.Resource{summary}, "kinesisanalyticsv2 applications")
			if ferr != nil {
				return total, inserted, apps, ferr
			}
			total += t
			inserted += i
			apps = append(apps, childParent{id: nm, arn: summary.NativeID})
			continue
		}
		appARN := sv(d.ApplicationARN)
		// The detail body is the application row: resolvers read
		// ServiceExecutionRole and CloudWatchLoggingOptionDescriptions from it.
		appStatus := string(d.ApplicationStatus)
		var subBatch []*store.Resource
		subBatch = append(subBatch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeKAV2Application, NativeID: appARN,
			Name: d.ApplicationName, Region: &region, Status: &appStatus,
			AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
		})
		for _, lo := range d.CloudWatchLoggingOptionDescriptions {
			id := sv(lo.CloudWatchLoggingOptionId)
			if id == "" {
				continue
			}
			arn := appARN + "/cloud-watch-logging-option/" + id
			label := id
			subBatch = append(subBatch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKAV2ApplicationCloudWatchLogOpt, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(lo), DiscoveredBy: scanID,
			})
		}
		if cfg := d.ApplicationConfigurationDescription; cfg != nil && cfg.SqlApplicationConfigurationDescription != nil {
			sqlcfg := cfg.SqlApplicationConfigurationDescription
			for _, o := range sqlcfg.OutputDescriptions {
				id := sv(o.OutputId)
				if id == "" {
					continue
				}
				arn := appARN + "/output/" + id
				subBatch = append(subBatch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeKAV2ApplicationOutput, NativeID: arn,
					Name: o.Name, Region: &region,
					AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
				})
			}
			for _, r := range sqlcfg.ReferenceDataSourceDescriptions {
				id := sv(r.ReferenceId)
				if id == "" {
					continue
				}
				arn := appARN + "/reference-data-source/" + id
				subBatch = append(subBatch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeKAV2ApplicationReferenceData, NativeID: arn,
					Name: r.TableName, Region: &region,
					AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				})
			}
		}
		t, i, ferr := upsertBatch(st, subBatch, "kinesisanalyticsv2 application-children")
		if ferr != nil {
			return total, inserted, apps, ferr
		}
		total += t
		inserted += i
		apps = append(apps, childParent{id: nm, arn: appARN})
	}
	return total, inserted, apps, errors.Join(describeErrs...)
}

// isKAV2AppGone reports whether err says the application no longer exists,
// i.e. it was deleted after ListApplications listed it.
func isKAV2AppGone(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// kav2SnapshotNativeID keys a snapshot under its application: the API returns
// no snapshot ARN, and the Service Reference defines ARNs only for
// applications. The name's model pattern (^[a-zA-Z0-9_.-]+$) admits no "/",
// and it goes last all the same.
func kav2SnapshotNativeID(appARN, name string) string {
	return appARN + "/snapshot/" + name
}

// scanKAV2ApplicationSnapshots stores each application's snapshots, a page at
// a time, and links each under its application. apps holds only applications
// stored this scan. Per application: ResourceNotFoundException (deleted since
// listing) is skipped silently, a denial warns once, a transient error after
// the SDK's retries is skipped with one warning for all such applications,
// UnsupportedOperationException and InvalidArgumentException are each skipped
// with one warning for all applications refused with that code, and anything
// else fails the phase. Rows an application stored before its error stay and
// are counted.
//
// The model omits ResourceNotFoundException for ListApplicationSnapshots, but
// every sibling op keyed on an application name (ListApplicationOperations,
// ListApplicationVersions, DescribeApplicationSnapshot) models it, so its
// absence is read as a model gap rather than proof the code cannot occur.
//
// The model lists the other two codes without saying when either is
// returned. Snapshots are a Managed Service for Apache
// Flink feature, and how the call answers for a SQL application, or for one
// in a transitional state, has not been observed; failing the phase would
// fail it on every scan, and skipping silently would hide a real error. An
// InvalidArgumentException provoked by the page size disco sets cannot be told
// apart by code, so it would warn rather than fail.
func scanKAV2ApplicationSnapshots(ctx context.Context, client kinesisanalyticsv2.ListApplicationSnapshotsAPIClient, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return childFanOutStoredSkipNotReady(ctx, st, acct, region, kav2ListApplicationSnapshotsOp, apps, isKAV2AppGone, byParentID,
		"application(s)", "snapshots may not be supported for the application's runtime or state",
		[]string{"UnsupportedOperationException", "InvalidArgumentException"},
		func(ctx context.Context, app childParent) (int, int, error) {
			return storeKAV2ApplicationSnapshots(ctx, client, acct, region, st, scanID, app)
		})
}

// storeKAV2ApplicationSnapshots stores one application's snapshots a page at a time.
func storeKAV2ApplicationSnapshots(ctx context.Context, client kinesisanalyticsv2.ListApplicationSnapshotsAPIClient, acct *account, region string, st *store.Store, scanID string, app childParent) (total, inserted int, err error) {
	pager := kinesisanalyticsv2.NewListApplicationSnapshotsPaginator(client,
		&kinesisanalyticsv2.ListApplicationSnapshotsInput{ApplicationName: &app.id},
		func(o *kinesisanalyticsv2.ListApplicationSnapshotsPaginatorOptions) {
			o.Limit = kav2SnapshotPageLimit
			o.StopOnDuplicateToken = true
		})
	parentID := store.ResourceID("aws", acct.ID, app.arn)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return total, inserted, err
		}
		var children []containedChild
		for _, s := range out.SnapshotSummaries {
			name := sv(s.SnapshotName)
			if name == "" {
				continue
			}
			children = append(children, containedChild{parentID: parentID, row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKAV2ApplicationSnapshot, NativeID: kav2SnapshotNativeID(app.arn, name),
				Name: &name, Region: &region, Status: nonEmptyPtr(string(s.SnapshotStatus)),
				CreatedAt:      tp(s.SnapshotCreationTimestamp),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}})
		}
		t, i, err := upsertContained(st, kav2ListApplicationSnapshotsOp, children)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}
