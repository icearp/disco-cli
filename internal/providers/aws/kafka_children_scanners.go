package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/icearp/disco-cli/store"
)

// kafkaChildAPI is the set of MSK operations the cluster child phases call.
type kafkaChildAPI interface {
	ListTopics(context.Context, *kafka.ListTopicsInput, ...func(*kafka.Options)) (*kafka.ListTopicsOutput, error)
	ListChannels(context.Context, *kafka.ListChannelsInput, ...func(*kafka.Options)) (*kafka.ListChannelsOutput, error)
	DescribeChannel(context.Context, *kafka.DescribeChannelInput, ...func(*kafka.Options)) (*kafka.DescribeChannelOutput, error)
	ListTagsForResource(context.Context, *kafka.ListTagsForResourceInput, ...func(*kafka.Options)) (*kafka.ListTagsForResourceOutput, error)
	ListClientVpcConnections(context.Context, *kafka.ListClientVpcConnectionsInput, ...func(*kafka.Options)) (*kafka.ListClientVpcConnectionsOutput, error)
	ListNodes(context.Context, *kafka.ListNodesInput, ...func(*kafka.Options)) (*kafka.ListNodesOutput, error)
}

const (
	kafkaListTopicsOp               = "kafka:ListTopics"
	kafkaListChannelsOp             = "kafka:ListChannels"
	kafkaDescribeChannelOp          = "kafka:DescribeChannel"
	kafkaListTagsForResourceOp      = "kafka:ListTagsForResource"
	kafkaListClientVpcConnectionsOp = "kafka:ListClientVpcConnections"
	kafkaListNodesOp                = "kafka:ListNodes"
)

// kafkaPageLimit is the MaxResults cap the MSK model documents for its list ops.
const kafkaPageLimit = 100

// kafkaClientVpcConnectionNativeID keys the cluster owner's record of a client
// VPC connection under its cluster. The connection's own ARN is not used
// alone: it is also the NativeID of the client account's aws:kafka:vpc-connection
// row, so if one account can own both the cluster and the client VPC (not
// verified) the two rows would merge. The ARN contains "/", so it is the last
// segment.
func kafkaClientVpcConnectionNativeID(clusterARN, vpcConnectionARN string) string {
	return clusterARN + "/client-vpc-connection/" + vpcConnectionARN
}

// kafkaSystemTopics are the topics that appear without a user call: Apache
// Kafka's internal topics (Topic.INTERNAL_TOPICS: consumer offsets and
// transaction state, created by the brokers on first use; kafka-topics
// refuses to delete them) and the canary topics MSK creates for its own
// availability monitoring. The MSK names come from MSK documentation and user
// reports, not from the API model, and have not been verified live.
var kafkaSystemTopics = map[string]bool{
	"__consumer_offsets":        true,
	"__transaction_state":       true,
	"__amazon_msk_canary":       true,
	"__amazon_msk_canary_state": true,
}

// kafkaParents returns the clusters keep accepts as child-phase parents.
func kafkaParents(clusters []kafkaCluster, keep func(kafkaCluster) bool) []childParent {
	var parents []childParent
	for _, c := range clusters {
		if keep(c) {
			parents = append(parents, childParent{id: c.arn, arn: c.arn})
		}
	}
	return parents
}

// isKafkaRejected reports whether err is a BadRequestException about the
// resource rather than about an input disco chose itself: one whose
// InvalidParameter names the page size or token is disco's own bug and is not
// rejected but returned. The parameter names are the model's query names;
// that MSK reports them in that form is not verified.
func isKafkaRejected(err error) bool {
	var br *kafkatypes.BadRequestException
	if !errors.As(err, &br) {
		return false
	}
	p := sv(br.InvalidParameter)
	return !strings.EqualFold(p, "maxResults") && !strings.EqualFold(p, "nextToken")
}

// warnKafkaRejected warns once for every rejected parent collected (kind names
// what the parents are), naming the first and its error. rejected reuses
// transientTally only for its counting; the warning is not a transient one.
func warnKafkaRejected(st *store.Store, op, kind string, acct *account, region string, rejected *transientTally) {
	if rejected.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d %s skipped with BadRequestException; it may not support this operation or may not be ready, first %s: %v", rejected.count, kind, rejected.first, rejected.err),
	})
}

// kafkaChildErrs is the per-cluster error policy of the child phases, on top
// of fanOutErrs: an error gone accepts (the cluster was deleted since listing)
// is skipped silently, a transient error after the SDK's retries is skipped
// with one warning for all such clusters, and a rejection (isKafkaRejected) is
// skipped with one warning for all such clusters.
//
// gone is isKafkaGone only for an op whose model lists NotFoundException
// (ListChannels, ListNodes); for one whose model does not (ListTopics,
// ListClientVpcConnections) it is neverSkip, so a NotFoundException there
// fails the phase like any other unexpected error.
//
// BadRequestException is the only client error, besides the authorization
// codes, that the model lists for every child op, and which clusters each op
// serves (cluster type, broker type, Kafka version, state) is not documented
// and has not been observed; failing the phase would fail it on every scan of
// an account with such a cluster, and skipping silently would hide a real
// input error, so it warns and keeps the siblings.
type kafkaChildErrs struct {
	op        string
	gone      func(error) bool
	transient transientTally
	rejected  transientTally
	// partial counts the clusters whose listing failed transiently or was
	// rejected after earlier pages were read; those pages are kept.
	partial skipTally
}

func isKafkaGone(err error) bool { return isAPIErrorCode(err, "NotFoundException") }

// tallied reports whether err was counted as transient or rejected for
// parent; kept says whether pages read before err are kept, making the
// parent partially read rather than skipped.
func (e *kafkaChildErrs) tallied(parent string, err error, kept bool) bool {
	switch {
	case !isTransientNetworkError(err) && !isKafkaRejected(err):
		return false
	case kept:
		e.partial.skipped(parent, err)
	case isTransientNetworkError(err):
		e.transient.skipped(parent, err)
	default:
		e.rejected.skipped(parent, err)
	}
	return true
}

func (e *kafkaChildErrs) warn(st *store.Store, acct *account, region string) {
	e.transient.warnSkipped(st, e.op, acct, region)
	warnKafkaRejected(st, e.op, "cluster(s)", acct, region, &e.rejected)
	if e.partial.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: e.op, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d cluster(s) partially read: a later page failed and only the pages before it were read, first %s: %v", e.partial.count, e.partial.first, e.partial.err),
		})
	}
}

// kafkaChildFanOut runs list for each parent through childFanOut under the
// kafkaChildErrs policy. A skipped parent keeps the rows list returned with
// its error (pages read before a later page failed); childFanOut drops those
// of a parent whose error it returns.
func kafkaChildFanOut(
	ctx context.Context, st *store.Store, acct *account, region string, errs *kafkaChildErrs, parents []childParent,
	list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	t, i, err := childFanOut(ctx, st, acct, region, errs.op, parents, errs.gone, func(ctx context.Context, p childParent) ([]*store.Resource, error) {
		rows, err := list(ctx, p)
		if err != nil && errs.tallied(p.id, err, len(rows) > 0) {
			return rows, nil
		}
		return rows, err
	})
	errs.warn(st, acct, region)
	return t, i, err
}

// scanKafkaTopics stores every cluster's topics a page at a time, since a
// cluster can hold thousands. The row is the ListTopics element:
// DescribeTopic adds only the topic's config and state, at one call per topic.
// Its OutOfSyncReplicaCount is kept as reported, so a topic's row changes
// while its replicas lag; it is real state, not a value rotated per read.
// A topic without a TopicArn is not stored.
func scanKafkaTopics(ctx context.Context, client kafkaChildAPI, acct *account, region string, st *store.Store, scanID string, clusters []kafkaCluster) (int, int, error) {
	errs := &kafkaChildErrs{op: kafkaListTopicsOp, gone: neverSkip}
	parents := kafkaParents(clusters, func(kafkaCluster) bool { return true })
	t, i, err := childFanOutStored(ctx, st, acct, region, errs.op, parents, errs.gone, func(ctx context.Context, p childParent) (int, int, error) {
		t, i, err := storeKafkaTopics(ctx, client, acct, region, st, scanID, p)
		if err != nil && errs.tallied(p.id, err, t > 0) {
			return t, i, nil
		}
		return t, i, err
	})
	errs.warn(st, acct, region)
	return t, i, err
}

// storeKafkaTopics stores one cluster's topics, each page as it arrives, and
// returns the counts of the pages stored before any error.
func storeKafkaTopics(ctx context.Context, client kafkaChildAPI, acct *account, region string, st *store.Store, scanID string, cluster childParent) (total, inserted int, err error) {
	parentID := store.ResourceID("aws", acct.ID, cluster.arn)
	pager := kafka.NewListTopicsPaginator(client,
		&kafka.ListTopicsInput{ClusterArn: sdkaws.String(cluster.arn)},
		func(o *kafka.ListTopicsPaginatorOptions) { o.Limit = kafkaPageLimit })
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return total, inserted, err
		}
		children := make([]containedChild, 0, len(out.Topics))
		for _, topic := range out.Topics {
			arn := sv(topic.TopicArn)
			if arn == "" {
				continue
			}
			children = append(children, containedChild{parentID: parentID, row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMSKTopic, NativeID: arn,
				Name: topic.TopicName, Region: &region,
				AttributesJSON: mustJSON(topic), DiscoveredBy: scanID,
				ManagedByProvider: kafkaSystemTopics[sv(topic.TopicName)],
			}})
		}
		t, i, err := upsertContained(st, kafkaListTopicsOp, children)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanKafkaChannels stores the channels of every Express cluster, the only
// kind that has them. The row is the DescribeChannel body: the ListChannels
// summary lacks the destination, encryption, logging and tags. A channel
// without a ChannelArn is not stored. Per channel, DescribeChannel's
// NotFoundException (deleted since listing) skips it silently and a rejection
// (isKafkaRejected) skips it with one warning for all such channels, keeping
// its siblings. A denied DescribeChannel, or one that still fails
// transiently after the SDK's retries, keeps the channel as its ListChannels
// summary, a different shape, with one warning per cause for the region. Its
// tags are then read with ListTagsForResource, so that a throttled describe
// does not read as an untagged channel; a NotFoundException there (in the
// op's model) means the channel was deleted and it is not stored, and any
// other failure stores it without tags, with one warning saying so.
func scanKafkaChannels(ctx context.Context, client kafkaChildAPI, acct *account, region string, st *store.Store, scanID string, clusters []kafkaCluster) (int, int, error) {
	errs := &kafkaChildErrs{op: kafkaListChannelsOp, gone: isKafkaGone}
	parents := kafkaParents(clusters, func(c kafkaCluster) bool { return c.express })
	var (
		describeDenied    skipTally
		describeRejected  transientTally
		describeTransient transientTally
		tagsUnread        skipTally
	)
	t, i, err := kafkaChildFanOut(ctx, st, acct, region, errs, parents, func(ctx context.Context, cluster childParent) ([]*store.Resource, error) {
		// A listing that failed after its first page returns the channels it
		// read with its error; they are described and returned with it, and
		// kafkaChildFanOut decides whether they are stored.
		listed, listErr := listKafkaChannels(ctx, client, cluster.arn)
		if len(listed) == 0 {
			return nil, listErr
		}
		// Described one at a time: clusters already run fanoutMed-wide, and a
		// cluster's channels are delivery pipelines, expected to be few.
		var rows []*store.Resource
		for _, c := range listed {
			arn := *c.ChannelArn
			out, err := client.DescribeChannel(ctx, &kafka.DescribeChannelInput{ClusterArn: sdkaws.String(cluster.arn), ChannelArn: sdkaws.String(arn)})
			switch {
			case err == nil:
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeMSKChannel, NativeID: arn,
					Name: out.ChannelName, Region: &region, Status: nonEmptyPtr(string(out.Status)),
					CreatedAt: tp(out.CreationTime), TagsJSON: mapTagsJSON(out.Tags),
					AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
				})
				continue
			case isKafkaGone(err):
				continue
			case isKafkaRejected(err):
				describeRejected.skipped(arn, err)
				continue
			case isAccessDenied(err), isTransientNetworkError(err):
			default:
				return nil, fmt.Errorf("%s %s: %w", kafkaDescribeChannelOp, arn, err)
			}
			// Denied or transient: kept as its ListChannels summary.
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMSKChannel, NativeID: arn,
				Name: c.ChannelName, Region: &region, Status: nonEmptyPtr(string(c.Status)),
				CreatedAt: tp(c.CreationTime), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}
			tags, terr := client.ListTagsForResource(ctx, &kafka.ListTagsForResourceInput{ResourceArn: sdkaws.String(arn)})
			switch {
			case terr == nil:
				r.TagsJSON = mapTagsJSON(tags.Tags)
			case isKafkaGone(terr):
				continue
			default:
				tagsUnread.skipped(arn, terr)
			}
			if isAccessDenied(err) {
				describeDenied.skipped(arn, err)
			} else {
				describeTransient.skipped(arn, err)
			}
			rows = append(rows, r)
		}
		return rows, listErr
	})
	warnKafkaRejected(st, kafkaDescribeChannelOp, "channel(s)", acct, region, &describeRejected)
	// The summaries are stored only when the fan-out stored its rows; on a
	// failed write, or a hard error in any cluster, it stored none.
	if t > 0 && !errors.Is(err, store.ErrStoreWrite) {
		const kept = "%d channel(s) stored as their ListChannels summary, without the DescribeChannel body, first %s: %w"
		if describeDenied.count > 0 {
			_ = skipIfAccessDenied(st, kafkaDescribeChannelOp, acct.ID, region, fmt.Errorf(kept, describeDenied.count, describeDenied.first, describeDenied.err))
		}
		if describeTransient.count > 0 {
			_ = skipIfTransient(st, kafkaDescribeChannelOp, acct.ID, region, fmt.Errorf(kept, describeTransient.count, describeTransient.first, describeTransient.err))
		}
		if tagsUnread.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: kafkaListTagsForResourceOp, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d channel(s) stored as their ListChannels summary with their tags unread, first %s: %v", tagsUnread.count, tagsUnread.first, tagsUnread.err),
			})
		}
	}
	return t, i, err
}

// listKafkaChannels lists one cluster's channels that carry a ChannelArn. On
// an error it returns the channels read before it with the error.
func listKafkaChannels(ctx context.Context, client kafkaChildAPI, clusterARN string) ([]kafkatypes.ChannelInfo, error) {
	in := &kafka.ListChannelsInput{ClusterArn: sdkaws.String(clusterARN), MaxResults: sdkaws.Int32(kafkaPageLimit)}
	var listed []kafkatypes.ChannelInfo
	for {
		out, err := client.ListChannels(ctx, in)
		if err != nil {
			return listed, err
		}
		for _, c := range out.Channels {
			if sv(c.ChannelArn) != "" {
				listed = append(listed, c)
			}
		}
		// ListChannels has no paginator; a repeated token would loop forever.
		if done, err := lastPage(in.NextToken, out.NextToken); done {
			return listed, err
		}
		in.NextToken = out.NextToken
	}
}

// scanKafkaClientVpcConnections stores, for every cluster, the multi-VPC
// connections client accounts made to it: the cluster owner's view of what the
// client account lists as aws:kafka:vpc-connection, carrying the client
// account (Owner) that view lacks. A connection without a VpcConnectionArn is
// not stored.
func scanKafkaClientVpcConnections(ctx context.Context, client kafkaChildAPI, acct *account, region string, st *store.Store, scanID string, clusters []kafkaCluster) (int, int, error) {
	errs := &kafkaChildErrs{op: kafkaListClientVpcConnectionsOp, gone: neverSkip}
	parents := kafkaParents(clusters, func(kafkaCluster) bool { return true })
	return kafkaChildFanOut(ctx, st, acct, region, errs, parents, func(ctx context.Context, cluster childParent) ([]*store.Resource, error) {
		pager := kafka.NewListClientVpcConnectionsPaginator(client,
			&kafka.ListClientVpcConnectionsInput{ClusterArn: sdkaws.String(cluster.arn)},
			func(o *kafka.ListClientVpcConnectionsPaginatorOptions) { o.Limit = kafkaPageLimit })
		var rows []*store.Resource
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, c := range out.ClientVpcConnections {
				arn := sv(c.VpcConnectionArn)
				if arn == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeMSKClientVpcConnection, NativeID: kafkaClientVpcConnectionNativeID(cluster.arn, arn),
					Region: &region, Status: nonEmptyPtr(string(c.State)), CreatedAt: tp(c.CreationTime),
					AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
}

// scanKafkaNodes stores the nodes of every provisioned cluster; a serverless
// cluster exposes no brokers. A node without a NodeARN is not stored.
func scanKafkaNodes(ctx context.Context, client kafkaChildAPI, acct *account, region string, st *store.Store, scanID string, clusters []kafkaCluster) (int, int, error) {
	errs := &kafkaChildErrs{op: kafkaListNodesOp, gone: isKafkaGone}
	parents := kafkaParents(clusters, func(c kafkaCluster) bool { return c.clusterType == kafkatypes.ClusterTypeProvisioned })
	return kafkaChildFanOut(ctx, st, acct, region, errs, parents, func(ctx context.Context, cluster childParent) ([]*store.Resource, error) {
		pager := kafka.NewListNodesPaginator(client,
			&kafka.ListNodesInput{ClusterArn: sdkaws.String(cluster.arn)},
			func(o *kafka.ListNodesPaginatorOptions) { o.Limit = kafkaPageLimit })
		var rows []*store.Resource
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, n := range out.NodeInfoList {
				arn := sv(n.NodeARN)
				if arn == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeMSKNode, NativeID: arn, Region: &region,
					AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
}
