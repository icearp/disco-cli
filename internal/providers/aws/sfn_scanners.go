package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:sfn", fn: scanSFN}) }

// scanSFN discovers Step Functions state machines and activities in one region.
// State machines are list-then-described concurrently so the full Definition
// (state DAG) lands in attributes for resolvers.
func scanSFN(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sfn.NewFromConfig(acct.cfg, func(o *sfn.Options) { o.Region = region })

	// Phase 1: list state machine ARNs.
	var smARNs []string
	smPager := sfn.NewListStateMachinesPaginator(client, &sfn.ListStateMachinesInput{})
	for smPager.HasMorePages() {
		out, err := smPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "states:ListStateMachines", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("states:ListStateMachines: %w", err)
		}
		for _, sm := range out.StateMachines {
			smARNs = append(smARNs, sv(sm.StateMachineArn))
		}
	}

	// Phase 2: describe each state machine concurrently.
	var (
		mu      sync.Mutex
		smBatch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, arn := range smARNs {
		g.Go(func() error {
			out, err := client.DescribeStateMachine(gctx, &sfn.DescribeStateMachineInput{StateMachineArn: &arn})
			if err != nil {
				if isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("states:DescribeStateMachine %s: %w", arn, err)
			}
			status := string(out.Status)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSFNStateMachine,
				NativeID:       arn,
				Name:           out.Name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			smBatch = append(smBatch, r)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(smBatch) > 0 {
		n, err := st.UpsertResources(smBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert SFN state machines: %w", err)
		}
		total += len(smBatch)
		inserted += n
	}

	// Phase 3: activities.
	var actBatch []*store.Resource
	actPager := sfn.NewListActivitiesPaginator(client, &sfn.ListActivitiesInput{})
	for actPager.HasMorePages() {
		out, err := actPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				break
			}
			return 0, 0, fmt.Errorf("states:ListActivities: %w", err)
		}
		for _, a := range out.Activities {
			arn := sv(a.ActivityArn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSFNActivity,
				NativeID:       arn,
				Name:           a.Name,
				Region:         &region,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}
			actBatch = append(actBatch, r)
		}
	}
	if len(actBatch) > 0 {
		n, err := st.UpsertResources(actBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert SFN activities: %w", err)
		}
		total += len(actBatch)
		inserted += n
	}

	return total, inserted, nil
}
