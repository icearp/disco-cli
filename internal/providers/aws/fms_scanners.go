package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/fms"
)

// isFMSNotEnabled disambiguates the "no default admin" not-onboarded state
// from a real IAM denial.
func isFMSNotEnabled(err error) bool {
	return isAccessDenied(err) && strings.Contains(err.Error(), "No default admin could be found")
}

// isFMSAdminOnlyDenial matches the AccessDeniedException AWS returns when
// a non-FMS-admin account calls a management-only op (ListPolicies,
// ListResourceSets). Distinct from isFMSNotEnabled — admin exists, just
// elsewhere in the org. Member accounts hit this every scan; silent-skip.
func isFMSAdminOnlyDenial(err error) bool {
	return isAccessDenied(err) &&
		strings.Contains(err.Error(), "only available to AWS Firewall Manager Administrators")
}

func init() {
	registerService(serviceEntry{
		name: "aws:fms",
		fn:   scanFMS,
		emits: []coverage.TypeDecl{
			{Service: "fms", DiscoType: TypeFMSNotificationChannel},
			{Service: "fms", DiscoType: TypeFMSPolicy},
			{Service: "fms", DiscoType: TypeFMSResourceSet},
		},
	})
}

type fmsAPI interface {
	GetNotificationChannel(context.Context, *fms.GetNotificationChannelInput, ...func(*fms.Options)) (*fms.GetNotificationChannelOutput, error)
	ListPolicies(context.Context, *fms.ListPoliciesInput, ...func(*fms.Options)) (*fms.ListPoliciesOutput, error)
	ListResourceSets(context.Context, *fms.ListResourceSetsInput, ...func(*fms.Options)) (*fms.ListResourceSetsOutput, error)
}

// scanFMS discovers Firewall Manager policies, resource sets, and the
// per-org notification channel. Service is global; callable only from
// us-east-1.
func scanFMS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if region != "us-east-1" {
		return 0, 0, nil
	}
	client := fms.NewFromConfig(acct.cfg, func(o *fms.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanFMSPolicies(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFMSResourceSets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFMSNotificationChannel(ctx, client, acct, region, st, scanID) },
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

func scanFMSPolicies(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fms.NewListPoliciesPaginator(client, &fms.ListPoliciesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isFMSNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isFMSAdminOnlyDenial(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fms:ListPolicies", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fms:ListPolicies: %w", err)
		}
		for _, p := range out.PolicyList {
			arn := sv(p.PolicyArn)
			if arn == "" {
				continue
			}
			status := string(p.PolicyStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSPolicy, NativeID: arn,
				Name: p.PolicyName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fms policies")
}

func scanFMSResourceSets(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListResourceSets(ctx, &fms.ListResourceSetsInput{NextToken: nextToken})
		if err != nil {
			if isFMSNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isFMSAdminOnlyDenial(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fms:ListResourceSets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fms:ListResourceSets: %w", err)
		}
		for _, r := range out.ResourceSets {
			id := sv(r.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:fms:%s:%s:resource-set/%s", region, acct.ID, id)
			status := string(r.ResourceSetStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSResourceSet, NativeID: arn,
				Name: r.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "fms resource-sets")
}

// scanFMSNotificationChannel captures the per-account notification channel
// (singleton). Synth ARN keyed on the SNS topic ARN.
func scanFMSNotificationChannel(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetNotificationChannel(ctx, &fms.GetNotificationChannelInput{})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("fms:GetNotificationChannel: %w", err)
	}
	topic := sv(out.SnsTopicArn)
	if topic == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:fms::%s:notification-channel", acct.ID)
	label := topic
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeFMSNotificationChannel, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "fms notification-channel")
}
