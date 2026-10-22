package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/mq"
	mqtypes "github.com/aws/aws-sdk-go-v2/service/mq/types"
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
	mqDescribeBrokerOp          = "mq:DescribeBroker"
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
// shared-resource phases need the brokers it stored. Only a failed store
// write in the broker phase returns at once: after any other broker error the
// later phases run with the brokers that were stored. The later phases are
// independent, so each runs whatever the others return, and all the phases'
// errors are joined. The broker phase (a ListBrokers error) and the
// configurations phase can return a transient error, which is demoted to a
// warning only beside a hard error, so it cannot hide another phase's hard
// error. The association phase fails only on store writes, and the
// shared-resource phase already skips transient brokers.
func scanMQ(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mq.NewFromConfig(acct.cfg, func(o *mq.Options) { o.Region = region })
	bTotal, bInserted, brokers, assocs, bErr := scanMQBrokers(ctx, client, acct, region, st, scanID)
	if errors.Is(bErr, store.ErrStoreWrite) {
		return bTotal, bInserted, bErr
	}
	cTotal, cInserted, cErr := scanMQConfigurations(ctx, client, acct, region, st, scanID)
	aTotal, aInserted, aErr := scanMQConfigurationAssociations(acct, region, st, scanID, assocs)
	sTotal, sInserted, sErr := scanMQSharedResources(ctx, client, acct, region, st, scanID, brokers)
	var errs phaseErrs
	errs.add("mq:ListBrokers", bErr)
	errs.add("mq:ListConfigurations", cErr)
	errs.add("mq configuration associations", aErr)
	errs.add("mq shared resources", sErr)
	return bTotal + cTotal + aTotal + sTotal, bInserted + cInserted + aInserted + sInserted, errs.join(st, acct, region)
}

// mqBrokerAssoc captures the (brokerARN, configId) pair surfaced by
// DescribeBroker.Configurations.Current. Collected during the broker phase and
// emitted as ConfigurationAssociation rows in the third phase.
type mqBrokerAssoc struct {
	brokerARN string
	configID  string
}

// mqBrokerReads tallies the listed brokers that have no identifiers or that
// DescribeBroker could not read, for one warning per cause after the rows
// are stored.
type mqBrokerReads struct {
	denied    skipTally
	transient transientTally
	noID      skipTally
	mu        sync.Mutex
	errs      []error
}

func (r *mqBrokerReads) failed(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

// warn reports each tally once; call it only after the brokers are stored.
func (r *mqBrokerReads) warn(st *store.Store, acct *account, region string) {
	const kept = "%d broker(s) stored as their ListBrokers summary, without the DescribeBroker body (tags, configuration, " +
		"security groups, subnets, KMS key), first %s: %w"
	if r.denied.count > 0 {
		_ = skipIfAccessDenied(st, mqDescribeBrokerOp, acct.ID, region, fmt.Errorf(kept, r.denied.count, r.denied.first, r.denied.err))
	}
	if r.transient.count > 0 {
		_ = skipIfTransient(st, mqDescribeBrokerOp, acct.ID, region, fmt.Errorf(kept, r.transient.count, r.transient.first, r.transient.err))
	}
}

// scanMQBrokers stores every listed broker and returns the stored brokers with
// their configuration associations. A broker's row is its DescribeBroker body.
// When DescribeBroker fails with anything but NotFoundException, the broker is
// stored as its ListBrokers summary instead. The summary lacks the tags, the
// configuration (so there is no association) and the security-group, subnet
// and KMS references the resolver reads. A denied or still-throttled read is
// warned once per cause. Any other DescribeBroker error is returned after the
// rows are stored. NotFoundException (in the op's model) means the broker was
// deleted after it was listed, so it is skipped. A broker listed without a
// BrokerArn or BrokerId is warned about and not stored. A denied listing keeps
// the pages read before it and warns. Any other listing error, transient
// included, is returned after those pages are stored. The returned brokers
// and associations are nil only when the store write fails.
func scanMQBrokers(ctx context.Context, client mqAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, []mqBrokerAssoc, error) {
	var (
		summaries []mqtypes.BrokerSummary
		listErr   error
	)
	p := mq.NewListBrokersPaginator(client, &mq.ListBrokersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mq:ListBrokers", acct.ID, region, err)
			} else {
				listErr = fmt.Errorf("mq:ListBrokers: %w", err)
			}
			break
		}
		summaries = append(summaries, page.BrokerSummaries...)
	}

	var (
		reads   mqBrokerReads
		mu      sync.Mutex
		rows    []*store.Resource
		brokers []childParent
		assocs  []mqBrokerAssoc
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, fanoutMed)
	for _, b := range summaries {
		arn, id := sv(b.BrokerArn), sv(b.BrokerId)
		if arn == "" || id == "" {
			reads.noID.skipped(fmt.Sprintf("%q", sv(b.BrokerName)), nil)
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			row, assoc, stored := mqBrokerRow(ctx, client, acct, region, scanID, b, &reads)
			if !stored {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			rows = append(rows, row)
			brokers = append(brokers, childParent{id: id, arn: arn})
			if assoc != nil {
				assocs = append(assocs, *assoc)
			}
		})
	}
	wg.Wait()

	readErr := errors.Join(append([]error{listErr}, reads.errs...)...)
	total, inserted, err := upsertBatch(st, rows, "mq brokers")
	if err != nil {
		return total, inserted, nil, nil, errors.Join(readErr, err)
	}
	reads.warn(st, acct, region)
	warnNotStored(st, "mq:ListBrokers", acct, region, &reads.noID, "brokers have no BrokerArn or BrokerId")
	return total, inserted, brokers, assocs, readErr
}

// mqBrokerRow builds a listed broker's row from DescribeBroker, or from its
// summary when that read fails (see scanMQBrokers). stored is false only for
// a broker DescribeBroker no longer finds.
func mqBrokerRow(ctx context.Context, client mqAPI, acct *account, region, scanID string, b mqtypes.BrokerSummary, reads *mqBrokerReads) (row *store.Resource, assoc *mqBrokerAssoc, stored bool) {
	arn := sv(b.BrokerArn)
	row = &store.Resource{
		Provider:     "aws",
		AccountID:    acct.ID,
		AccountName:  &acct.Name,
		Type:         TypeMQBroker,
		NativeID:     arn,
		Region:       &region,
		DiscoveredBy: scanID,
	}
	out, err := client.DescribeBroker(ctx, &mq.DescribeBrokerInput{BrokerId: b.BrokerId})
	switch {
	case err == nil:
		row.Name = out.BrokerName
		row.Status = nonEmptyPtr(string(out.BrokerState))
		row.CreatedAt = tp(out.Created)
		row.TagsJSON = mapTagsJSON(out.Tags)
		row.AttributesJSON = mustJSON(out)
		if out.Configurations != nil && out.Configurations.Current != nil {
			if cid := sv(out.Configurations.Current.Id); cid != "" {
				assoc = &mqBrokerAssoc{brokerARN: arn, configID: cid}
			}
		}
		return row, assoc, true
	case isAPIErrorCode(err, "NotFoundException"):
		return nil, nil, false
	case reads.transient.drop(arn, err):
		// tallied by drop
	case isAccessDenied(err):
		reads.denied.skipped(arn, err)
	default:
		reads.failed(fmt.Errorf("%s %s: %w", mqDescribeBrokerOp, sv(b.BrokerId), err))
	}
	row.Name = b.BrokerName
	row.Status = nonEmptyPtr(string(b.BrokerState))
	row.CreatedAt = tp(b.Created)
	row.AttributesJSON = mustJSON(b)
	return row, nil, true
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
				TagsJSON:       mapTagsJSON(c.Tags),
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
