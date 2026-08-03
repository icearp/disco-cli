package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/securitylake"
	sltypes "github.com/aws/aws-sdk-go-v2/service/securitylake/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isSecurityLakeNotEnabled disambiguates the not-enabled shapes from a real IAM
// denial. Real IAM denials always name the action ("User: arn:... is not
// authorized to perform: <action>"), so they still warn.
//
// AWS reports the SAME not-enabled account state in three phrasings across two
// error classes, and which one you get depends on the regional endpoint. Two
// arrive as an access-denied code: "must be a delegated Security Lake
// administrator account" (delegation prerequisite missing) and the canned
// action-less "account is not authorized to perform this operation" (never
// onboarded). The third arrives as a ResourceNotFoundException carrying HTTP
// 404 — "Security Lake isn't enabled for your account in any Regions" — which
// is why the access-denied check cannot be a precondition for all of them.
//
// Measured on one account with Security Lake off everywhere: us-east-1,
// us-east-2, eu-west-1 and ap-northeast-1 answered ListSubscribers with the
// delegated-administrator phrasing, while eu-west-2 alone answered
// ResourceNotFoundException. Identical account state, different regional
// answer — so a single unmatched region turned an account-level fact into a
// per-scan hard error that marked every scan partial. Compare
// isStorageLensHomeRegionGap, where AWS likewise phrases one fact two ways.
// Every branch reads the typed message via isAPIErrorWithMessage rather than
// err.Error(), so a change in how the SDK renders its wrappers cannot silently
// stop a branch matching.
func isSecurityLakeNotEnabled(err error) bool {
	return isAPIErrorWithMessage(err, "ResourceNotFoundException", "Security Lake isn't enabled for your account") ||
		isAccessDeniedWithMessage(err, "delegated Security Lake administrator") ||
		isAccessDeniedWithMessage(err, "account is not authorized to perform this operation")
}

func init() {
	registerType(restype.Descriptor{Type: TypeSecurityLakeDataLake, Service: "security-lake", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityLakeSubscriber, Service: "security-lake", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityLakeAwsLogSource, Service: "security-lake", Leaf: true})
	registerService(serviceEntry{
		name: "aws:security-lake",
		fn:   scanSecurityLake,
	})
}

type securityLakeAPI interface {
	ListDataLakes(context.Context, *securitylake.ListDataLakesInput, ...func(*securitylake.Options)) (*securitylake.ListDataLakesOutput, error)
	ListSubscribers(context.Context, *securitylake.ListSubscribersInput, ...func(*securitylake.Options)) (*securitylake.ListSubscribersOutput, error)
	ListLogSources(context.Context, *securitylake.ListLogSourcesInput, ...func(*securitylake.Options)) (*securitylake.ListLogSourcesOutput, error)
}

// scanSecurityLake discovers Security Lake data lakes (per-region), subscribers,
// and AWS log sources (the per-(account, region, source) flag).
//
// AWS::SecurityLake::SubscriberNotification is skipped: the SDK exposes only
// Create/Delete/Update, no list endpoint.
func scanSecurityLake(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := securitylake.NewFromConfig(acct.cfg, func(o *securitylake.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanSLDataLakes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSLSubscribers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSLAwsLogSources(ctx, client, acct, region, st, scanID) },
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

func scanSLDataLakes(ctx context.Context, client securityLakeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.ListDataLakes(ctx, &securitylake.ListDataLakesInput{Regions: []string{region}})
	if err != nil {
		if isSecurityLakeNotEnabled(err) {
			return 0, 0, markServiceDisabled(err)
		}
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "securitylake:ListDataLakes", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("securitylake:ListDataLakes: %w", err)
	}
	var batch []*store.Resource
	for _, d := range out.DataLakes {
		arn := sv(d.DataLakeArn)
		if arn == "" {
			continue
		}
		status := string(d.CreateStatus)
		label := arn
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeSecurityLakeDataLake, NativeID: arn,
			Name: &label, Region: &region, Status: &status,
			AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "securitylake data-lakes")
}

func scanSLSubscribers(ctx context.Context, client securityLakeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securitylake.NewListSubscribersPaginator(client, &securitylake.ListSubscribersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isSecurityLakeNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "securitylake:ListSubscribers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("securitylake:ListSubscribers: %w", err)
		}
		for _, s := range out.Subscribers {
			arn := sv(s.SubscriberArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityLakeSubscriber, NativeID: arn,
				Name: s.SubscriberName, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securitylake subscribers")
}

// scanSLAwsLogSources walks ListLogSources and emits one row per
// (account, region, source-name) tuple matching CFN's AwsLogSource shape.
// Synth ARN: arn:aws:securitylake:{region}:{account}:aws-log-source/{sourceName}.
func scanSLAwsLogSources(ctx context.Context, client securityLakeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securitylake.NewListLogSourcesPaginator(client, &securitylake.ListLogSourcesInput{Regions: []string{region}})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isSecurityLakeNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "securitylake:ListLogSources", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("securitylake:ListLogSources: %w", err)
		}
		for _, ls := range out.Sources {
			ownerAcct := sv(ls.Account)
			if ownerAcct == "" {
				ownerAcct = acct.ID
			}
			for _, src := range ls.Sources {
				aws, ok := src.(*sltypes.LogSourceResourceMemberAwsLogSource)
				if !ok {
					continue
				}
				name := string(aws.Value.SourceName)
				if name == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:securitylake:%s:%s:aws-log-source/%s", region, ownerAcct, name)
				label := name
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSecurityLakeAwsLogSource, NativeID: arn,
					Name: &label, Region: &region,
					AttributesJSON: mustJSON(aws.Value), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "securitylake aws-log-sources")
}
