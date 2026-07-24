package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/mq"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMQBroker, Service: "mq", Upstream: "AWS::AmazonMQ::Broker"})
	registerType(restype.Descriptor{Type: TypeMQConfiguration, Service: "mq", Upstream: "AWS::AmazonMQ::Configuration", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMQConfigurationAssociation, Service: "mq", Upstream: "AWS::AmazonMQ::ConfigurationAssociation"})
	registerService(serviceEntry{
		name: "aws:mq",
		fn:   scanMQ,
	})
}

// mqConfigurationAssociationNativeID synthesizes the broker→configuration
// association NativeID. CFN's AWS::AmazonMQ::ConfigurationAssociation has no
// AWS-issued ARN; uniqueness is the (brokerARN, configId) pair.
func mqConfigurationAssociationNativeID(brokerARN, configID string) string {
	return brokerARN + "/configuration-association/" + configID
}

// mqAPI is the narrow surface the MQ scanner uses. ListBrokers + DescribeBroker
// drive the broker phase; ListConfigurations carries the full Configuration
// body, so no DescribeConfiguration fan-out is needed.
type mqAPI interface {
	ListBrokers(context.Context, *mq.ListBrokersInput, ...func(*mq.Options)) (*mq.ListBrokersOutput, error)
	DescribeBroker(context.Context, *mq.DescribeBrokerInput, ...func(*mq.Options)) (*mq.DescribeBrokerOutput, error)
	ListConfigurations(context.Context, *mq.ListConfigurationsInput, ...func(*mq.Options)) (*mq.ListConfigurationsOutput, error)
}

func scanMQ(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mq.NewFromConfig(acct.cfg, func(o *mq.Options) { o.Region = region })
	bTotal, bInserted, assocs, err := scanMQBrokers(ctx, client, acct, region, st, scanID)
	if err != nil {
		return bTotal, bInserted, err
	}
	cTotal, cInserted, err := scanMQConfigurations(ctx, client, acct, region, st, scanID)
	if err != nil {
		return bTotal + cTotal, bInserted + cInserted, err
	}
	aTotal, aInserted, err := scanMQConfigurationAssociations(acct, region, st, scanID, assocs)
	if err != nil {
		return bTotal + cTotal + aTotal, bInserted + cInserted + aInserted, err
	}
	return bTotal + cTotal + aTotal, bInserted + cInserted + aInserted, nil
}

// mqBrokerAssoc captures the (brokerARN, configId) pair surfaced by
// DescribeBroker.Configurations.Current. Collected during the broker phase and
// emitted as ConfigurationAssociation rows in the third phase.
type mqBrokerAssoc struct {
	brokerARN string
	configID  string
}

func scanMQBrokers(ctx context.Context, client mqAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []mqBrokerAssoc, error) {
	p := mq.NewListBrokersPaginator(client, &mq.ListBrokersInput{})
	var (
		assocMu sync.Mutex
		assocs  []mqBrokerAssoc
	)
	total, inserted, err := pageScanConcurrent(ctx, "mq:ListBrokers", acct, region, st,
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
			if out.Configurations != nil && out.Configurations.Current != nil {
				if cid := sv(out.Configurations.Current.Id); cid != "" {
					assocMu.Lock()
					assocs = append(assocs, mqBrokerAssoc{brokerARN: arn, configID: cid})
					assocMu.Unlock()
				}
			}
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
	return total, inserted, assocs, err
}

// scanMQConfigurationAssociations emits one ConfigurationAssociation row per
// (broker, current-configuration) pair captured during the broker phase. The
// row carries no native ARN (CFN-only resource) — NativeID is synthesized via
// mqConfigurationAssociationNativeID and the row is closure-wired under its
// parent broker so graph walks reach it through the broker.
func scanMQConfigurationAssociations(acct *account, region string, st *store.Store, scanID string, assocs []mqBrokerAssoc) (total, inserted int, err error) {
	if len(assocs) == 0 {
		return 0, 0, nil
	}
	batch := make([]*store.Resource, 0, len(assocs))
	for _, a := range assocs {
		nativeID := mqConfigurationAssociationNativeID(a.brokerARN, a.configID)
		name := a.configID
		attrs := map[string]string{"Broker": a.brokerARN, "Configuration": a.configID}
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeMQConfigurationAssociation,
			NativeID:       nativeID,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(attrs),
			DiscoveredBy:   scanID,
		})
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert mq configuration-associations: %w", err)
	}
	pairs := make([][2]string, len(batch))
	for i, r := range batch {
		parentID := store.ResourceID("aws", acct.ID, assocs[i].brokerARN)
		pairs[i] = [2]string{r.ID, parentID}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure mq configuration-associations: %w", err)
	}
	return len(batch), n, nil
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
