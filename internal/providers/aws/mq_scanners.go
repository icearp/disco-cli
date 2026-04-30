package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/mq"
)

func init() {
	registerService(serviceEntry{
		name: "aws:mq",
		fn:   scanMQ,
		emits: []coverage.TypeDecl{
			{Service: "mq", DiscoType: TypeMQBroker},
			{Service: "mq", DiscoType: TypeMQConfiguration},
		},
	})
}

// mqAPI is the narrow surface the MQ scanner uses. ListBrokers + DescribeBroker
// drive the broker phase; ListConfigurations carries the full Configuration body
// so no DescribeConfiguration fan-out is needed for the configuration phase.
type mqAPI interface {
	ListBrokers(context.Context, *mq.ListBrokersInput, ...func(*mq.Options)) (*mq.ListBrokersOutput, error)
	DescribeBroker(context.Context, *mq.DescribeBrokerInput, ...func(*mq.Options)) (*mq.DescribeBrokerOutput, error)
	ListConfigurations(context.Context, *mq.ListConfigurationsInput, ...func(*mq.Options)) (*mq.ListConfigurationsOutput, error)
}

func scanMQ(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mq.NewFromConfig(acct.cfg, func(o *mq.Options) { o.Region = region })
	bTotal, bInserted, err := scanMQBrokers(ctx, client, acct, region, st, scanID)
	if err != nil {
		return bTotal, bInserted, err
	}
	cTotal, cInserted, err := scanMQConfigurations(ctx, client, acct, region, st, scanID)
	if err != nil {
		return bTotal + cTotal, bInserted + cInserted, err
	}
	return bTotal + cTotal, bInserted + cInserted, nil
}

func scanMQBrokers(ctx context.Context, client mqAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := mq.NewListBrokersPaginator(client, &mq.ListBrokersInput{})
	return pageScanConcurrent(ctx, "mq:ListBrokers", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*mq.ListBrokersOutput, error) { return p.NextPage(c) },
		func(o *mq.ListBrokersOutput) []string {
			out := make([]string, 0, len(o.BrokerSummaries))
			for _, b := range o.BrokerSummaries {
				out = append(out, sv(b.BrokerId))
			}
			return out
		},
		func(gctx context.Context, brokerID string) (*store.Resource, error) {
			if brokerID == "" {
				return nil, nil
			}
			out, err := client.DescribeBroker(gctx, &mq.DescribeBrokerInput{BrokerId: &brokerID})
			if err != nil {
				if isAccessDenied(err) {
					return nil, nil
				}
				return nil, err
			}
			arn := sv(out.BrokerArn)
			if arn == "" {
				return nil, nil
			}
			name := sv(out.BrokerName)
			status := string(out.BrokerState)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeMQBroker,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.Created),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		}, fanoutMed)
}

// scanMQConfigurations enumerates Amazon MQ broker configurations. The list
// API ships full Configuration bodies (no DescribeConfiguration fan-out).
// ListConfigurations is paginated via NextToken; SDK exposes no paginator
// helper for this op (per AWS-CLAUDE.md "SDK paginator availability per-op").
func scanMQConfigurations(ctx context.Context, client mqAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var nextToken *string
	for {
		out, err := client.ListConfigurations(ctx, &mq.ListConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "mq:ListConfigurations", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("mq:ListConfigurations: %w", err)
		}
		batch := make([]*store.Resource, 0, len(out.Configurations))
		for _, c := range out.Configurations {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			name := sv(c.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeMQConfiguration,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(c.Created),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert mq configurations: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return total, inserted, nil
}
