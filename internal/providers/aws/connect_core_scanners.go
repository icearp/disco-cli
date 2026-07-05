package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectInstance},
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectTrafficDistributionGroup},
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectPhoneNumber},
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectEmailAddress},
	)
}

// connectCoreAPI is the narrow surface used by the Core family — top-level
// resources (Instance), account-scoped sibling resources
// (TrafficDistributionGroup, PhoneNumber via V2), and per-instance
// EmailAddress.
type connectCoreAPI interface {
	connectInstanceLister
	DescribeInstance(context.Context, *connect.DescribeInstanceInput, ...func(*connect.Options)) (*connect.DescribeInstanceOutput, error)
	ListTrafficDistributionGroups(context.Context, *connect.ListTrafficDistributionGroupsInput, ...func(*connect.Options)) (*connect.ListTrafficDistributionGroupsOutput, error)
	DescribeTrafficDistributionGroup(context.Context, *connect.DescribeTrafficDistributionGroupInput, ...func(*connect.Options)) (*connect.DescribeTrafficDistributionGroupOutput, error)
	ListPhoneNumbersV2(context.Context, *connect.ListPhoneNumbersV2Input, ...func(*connect.Options)) (*connect.ListPhoneNumbersV2Output, error)
	DescribePhoneNumber(context.Context, *connect.DescribePhoneNumberInput, ...func(*connect.Options)) (*connect.DescribePhoneNumberOutput, error)
	SearchEmailAddresses(context.Context, *connect.SearchEmailAddressesInput, ...func(*connect.Options)) (*connect.SearchEmailAddressesOutput, error)
}

// scanConnectCore runs the account-scoped + per-instance Core phases:
// instances, traffic distribution groups, claimed phone numbers, email
// addresses (per instance).
func scanConnectCore(ctx context.Context, client connectCoreAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanConnectInstances(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectTrafficDistributionGroups(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanConnectPhoneNumbers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanConnectEmailAddresses(ctx, client, instances, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanConnectInstances fans out DescribeInstance for each instance summary
// from the pre-fetched list. NativeID = Instance ARN.
func scanConnectInstances(ctx context.Context, client connectCoreAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	ids := make([]string, 0, len(instances))
	for _, i := range instances {
		if i.Id != nil {
			ids = append(ids, *i.Id)
		}
	}
	return connectDescribeFanout(ctx, ids, fanoutMed, func(gctx context.Context, id string) (*store.Resource, error) {
		out, derr := client.DescribeInstance(gctx, &connect.DescribeInstanceInput{InstanceId: &id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeInstance %s: %w", id, derr)
		}
		if out.Instance == nil {
			return nil, nil
		}
		arn := sv(out.Instance.Arn)
		if arn == "" {
			return nil, nil
		}
		alias := sv(out.Instance.InstanceAlias)
		status := string(out.Instance.InstanceStatus)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectInstance,
			NativeID:       arn,
			Name:           &alias,
			Region:         &region,
			Status:         &status,
			CreatedAt:      tp(out.Instance.CreatedTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect instances")
}

// scanConnectTrafficDistributionGroups lists account-scoped TDGs then fans
// out Describe.
func scanConnectTrafficDistributionGroups(ctx context.Context, client connectCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := connect.NewListTrafficDistributionGroupsPaginator(client, &connect.ListTrafficDistributionGroupsInput{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			// Global-Resiliency traffic distribution needs a provisioned Connect
			// instance; without one AWS blocks the op via a resource-based-policy
			// explicit deny. Expected environmental state — silent-skip. (Distinct
			// from isSCPExplicitDeny, which matches "service control policy".)
			if isAccessDeniedWithMessage(perr, "explicit deny in a resource-based policy") {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "connect:ListTrafficDistributionGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("connect:ListTrafficDistributionGroups: %w", perr)
		}
		for _, t := range out.TrafficDistributionGroupSummaryList {
			if t.Id != nil {
				ids = append(ids, *t.Id)
			}
		}
	}
	return connectDescribeFanout(ctx, ids, fanoutMed, func(gctx context.Context, id string) (*store.Resource, error) {
		out, derr := client.DescribeTrafficDistributionGroup(gctx, &connect.DescribeTrafficDistributionGroupInput{TrafficDistributionGroupId: &id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeTrafficDistributionGroup %s: %w", id, derr)
		}
		if out.TrafficDistributionGroup == nil {
			return nil, nil
		}
		arn := sv(out.TrafficDistributionGroup.Arn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.TrafficDistributionGroup.Name)
		status := string(out.TrafficDistributionGroup.Status)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectTrafficDistributionGroup,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			Status:         &status,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect traffic distribution groups")
}

// scanConnectPhoneNumbers lists account-scoped phone numbers via V2 then
// fans out DescribePhoneNumber. ListPhoneNumbersV2 lists across all
// instances + TDGs in the region without an InstanceId filter.
func scanConnectPhoneNumbers(ctx context.Context, client connectCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := connect.NewListPhoneNumbersV2Paginator(client, &connect.ListPhoneNumbersV2Input{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "connect:ListPhoneNumbersV2", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("connect:ListPhoneNumbersV2: %w", perr)
		}
		for _, p := range out.ListPhoneNumbersSummaryList {
			if p.PhoneNumberId != nil {
				ids = append(ids, *p.PhoneNumberId)
			}
		}
	}
	return connectDescribeFanout(ctx, ids, fanoutMed, func(gctx context.Context, id string) (*store.Resource, error) {
		out, derr := client.DescribePhoneNumber(gctx, &connect.DescribePhoneNumberInput{PhoneNumberId: &id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribePhoneNumber %s: %w", id, derr)
		}
		if out.ClaimedPhoneNumberSummary == nil {
			return nil, nil
		}
		arn := sv(out.ClaimedPhoneNumberSummary.PhoneNumberArn)
		if arn == "" {
			return nil, nil
		}
		num := sv(out.ClaimedPhoneNumberSummary.PhoneNumber)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectPhoneNumber,
			NativeID:       arn,
			Name:           &num,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect phone numbers")
}

// scanConnectEmailAddresses fans out per instance — SearchEmailAddresses
// requires InstanceId. Each instance returns its own email-address pool.
// EmailAddressArn is in the search summary; no Describe op exists, so
// AttributesJSON = the search-summary entry.
func scanConnectEmailAddresses(ctx context.Context, client connectCoreAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		var token *string
		for {
			out, perr := client.SearchEmailAddresses(ctx, &connect.SearchEmailAddressesInput{InstanceId: &instID, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("connect:SearchEmailAddresses %s: %w", instID, perr)
			}
			for _, e := range out.EmailAddresses {
				arn := sv(e.EmailAddressArn)
				if arn == "" {
					continue
				}
				addr := sv(e.EmailAddress)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectEmailAddress,
					NativeID:       arn,
					Name:           &addr,
					Region:         &region,
					AttributesJSON: mustJSON(e),
					DiscoveredBy:   scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert connect email addresses: %w", uerr)
	}
	return len(batch), n, nil
}

// connectDescribeFanout mirrors sagemakerDescribeFanout — name-keyed
// concurrent Describe + collect + upsert. Kept distinct so the two
// services don't share an identifier helper across SDK boundaries.
func connectDescribeFanout(
	ctx context.Context,
	keys []string,
	weight int64,
	build func(context.Context, string) (*store.Resource, error),
	st *store.Store,
	label string,
) (int, int, error) {
	if len(keys) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(weight)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, k := range keys {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			r, err := build(gctx, k)
			if err != nil {
				return err
			}
			if r == nil {
				return nil
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert %s: %w", label, uerr)
	}
	return len(batch), n, nil
}
