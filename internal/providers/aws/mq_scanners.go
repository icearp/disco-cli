package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/mq"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMQBroker, Service: "mq"})
	registerType(restype.Descriptor{Type: TypeMQConfiguration, Service: "mq"})
	registerType(restype.Descriptor{Type: TypeMQConfigurationAssociation, Service: "mq"})
	registerType(restype.Descriptor{Type: TypeMQSharedResource, Service: "mq"})
	registerService(serviceEntry{
		name: "aws:mq",
		fn:   scanMQ,
	})
}

const (
	mqDescribeSharedResourcesOp = "mq:DescribeSharedResources"
	// mqSharedResourcesPageLimit is the documented MaxResults cap (5..100).
	mqSharedResourcesPageLimit = 100
)

// mqSharedResourceNativeID synthesizes a shared resource's NativeID. The API
// issues no ARN for the broker-side association. ResourceArn is assumed unique
// per broker: the model lists a resource's shares in the plural
// ResourceShareArns rather than repeating the resource (unverified).
// ResourceArn goes last because it contains "/" and ":".
func mqSharedResourceNativeID(brokerARN, resourceARN string) string {
	return brokerARN + "/shared-resource/" + resourceARN
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
	DescribeSharedResources(context.Context, *mq.DescribeSharedResourcesInput, ...func(*mq.Options)) (*mq.DescribeSharedResourcesOutput, error)
}

// scanMQ runs the broker phase first because the association and
// shared-resource phases need its brokers; a broker-phase error returns at
// once. The later phases are independent, so each runs whatever the others
// return and their errors are joined. Only the configurations phase can return
// a transient error, so only it is demoted to a warning, which keeps it from
// hiding another phase's hard error. The association phase fails only on store
// writes, and the shared-resource phase already skips transient brokers.
func scanMQ(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mq.NewFromConfig(acct.cfg, func(o *mq.Options) { o.Region = region })
	bTotal, bInserted, brokers, assocs, err := scanMQBrokers(ctx, client, acct, region, st, scanID)
	if err != nil {
		return bTotal, bInserted, err
	}
	cTotal, cInserted, cErr := scanMQConfigurations(ctx, client, acct, region, st, scanID)
	aTotal, aInserted, aErr := scanMQConfigurationAssociations(acct, region, st, scanID, assocs)
	sTotal, sInserted, sErr := scanMQSharedResources(ctx, client, acct, region, st, scanID, brokers)
	return bTotal + cTotal + aTotal + sTotal, bInserted + cInserted + aInserted + sInserted, errors.Join(
		demoteTransient(st, "mq:ListConfigurations", acct, region, cErr),
		aErr,
		sErr,
	)
}

// mqBrokerAssoc captures the (brokerARN, configId) pair surfaced by
// DescribeBroker.Configurations.Current. Collected during the broker phase and
// emitted as ConfigurationAssociation rows in the third phase.
type mqBrokerAssoc struct {
	brokerARN string
	configID  string
}

// scanMQBrokers stores the brokers and returns them with their configuration
// associations. The returned brokers are all stored only when err is nil:
// pageScanConcurrent upserts every page it lists and returns on the first
// failure, so scanMQ uses them only then.
func scanMQBrokers(ctx context.Context, client mqAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, []mqBrokerAssoc, error) {
	p := mq.NewListBrokersPaginator(client, &mq.ListBrokersInput{})
	var (
		mu      sync.Mutex
		brokers []childParent
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
			mu.Lock()
			brokers = append(brokers, childParent{id: brokerID, arn: arn})
			if out.Configurations != nil && out.Configurations.Current != nil {
				if cid := sv(out.Configurations.Current.Id); cid != "" {
					assocs = append(assocs, mqBrokerAssoc{brokerARN: arn, configID: cid})
				}
			}
			mu.Unlock()
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
	return total, inserted, brokers, assocs, err
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

// mqSharedResourceSkipCodes are the modeled DescribeSharedResources errors a
// broker is skipped on, with one warning, rather than failing the phase.
var mqSharedResourceSkipCodes = []skipCode{{
	code: "BadRequestException",
	why:  "the op models it and which brokers draw it (engine, deployment mode, broker state) has not been observed",
}}

// scanMQSharedResources stores the resources shared to each broker the broker
// phase stored, under that broker. A NotFoundException (modeled; a broker
// deleted after ListBrokers listed it) skips the broker silently, unless every
// broker drew one: then one warning says so, because a region whose endpoint
// does not serve the op may answer that way for all of them (unverified). A
// denial, a BadRequestException or a transient failure skips the broker with
// one warning per kind; anything else fails the phase. Rows a broker stored
// before its error stay and are counted.
func scanMQSharedResources(ctx context.Context, client mqAPI, acct *account, region string, st *store.Store, scanID string, brokers []childParent) (int, int, error) {
	var gone skipTally
	total, inserted, err := childFanOutStoredSkipCodes(ctx, st, acct, region, mqDescribeSharedResourcesOp, brokers,
		func(error) bool { return false }, byParentID, "broker(s)", mqSharedResourceSkipCodes,
		func(ctx context.Context, b childParent) (int, int, error) {
			t, i, err := storeMQSharedResources(ctx, client, acct, region, st, scanID, b)
			if gone.dropCode("NotFoundException", b.id, err) {
				return t, i, nil
			}
			return t, i, err
		})
	if gone.count == len(brokers) { // warnCode is a no-op for a zero count
		gone.warnCode(st, mqDescribeSharedResourcesOp, acct, region, "NotFoundException", "broker(s)",
			"every broker listed drew it, so the op may not be served in this region (unverified)")
	}
	return total, inserted, err
}

// storeMQSharedResources stores one broker's shared resources a page at a time.
func storeMQSharedResources(ctx context.Context, client mqAPI, acct *account, region string, st *store.Store, scanID string, broker childParent) (total, inserted int, err error) {
	pager := mq.NewDescribeSharedResourcesPaginator(client,
		&mq.DescribeSharedResourcesInput{BrokerId: &broker.id},
		func(o *mq.DescribeSharedResourcesPaginatorOptions) {
			o.Limit = mqSharedResourcesPageLimit
			o.StopOnDuplicateToken = true
		})
	parentID := store.ResourceID("aws", acct.ID, broker.arn)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return total, inserted, err
		}
		children := make([]containedChild, 0, len(out.SharedResources))
		for _, r := range out.SharedResources {
			resourceARN := sv(r.ResourceArn)
			if resourceARN == "" {
				continue
			}
			children = append(children, containedChild{row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMQSharedResource, NativeID: mqSharedResourceNativeID(broker.arn, resourceARN),
				Name: &resourceARN, Region: &region, Status: nonEmptyPtr(string(r.Status)),
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			}, parentID: parentID})
		}
		t, i, err := upsertContained(st, mqDescribeSharedResourcesOp, children)
		total += t
		inserted += i
		if err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}
