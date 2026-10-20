package aws

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/icearp/disco-cli/store"
)

const (
	kafkaTestClusterA = "arn:aws:kafka:us-east-1:123456789012:cluster/std/aaaaaaaa-1111-2222-3333-444444444444-1"
	kafkaTestClusterX = "arn:aws:kafka:us-east-1:123456789012:cluster/exp/bbbbbbbb-1111-2222-3333-444444444444-1"
	kafkaTestClusterS = "arn:aws:kafka:us-east-1:123456789012:cluster/srv/cccccccc-1111-2222-3333-444444444444-s1"
	kafkaTestStale    = "arn:aws:kafka:us-east-1:123456789012:cluster/old/dddddddd-1111-2222-3333-444444444444-1"

	kafkaTestTopicA1   = "arn:aws:kafka:us-east-1:123456789012:topic/std/aaaaaaaa-1111-2222-3333-444444444444-1/orders.v1"
	kafkaTestTopicA2   = "arn:aws:kafka:us-east-1:123456789012:topic/std/aaaaaaaa-1111-2222-3333-444444444444-1/payments_v2"
	kafkaTestTopicX1   = "arn:aws:kafka:us-east-1:123456789012:topic/exp/bbbbbbbb-1111-2222-3333-444444444444-1/clicks"
	kafkaTestTopicXSys = "arn:aws:kafka:us-east-1:123456789012:topic/exp/bbbbbbbb-1111-2222-3333-444444444444-1/__consumer_offsets"

	kafkaTestChannel1 = "arn:aws:kafka:us-east-1:123456789012:channel/exp/bbbbbbbb-1111-2222-3333-444444444444-1/to-s3/eeee-1"
	kafkaTestChannel2 = "arn:aws:kafka:us-east-1:123456789012:channel/exp/bbbbbbbb-1111-2222-3333-444444444444-1/to-ice/eeee-2"

	kafkaTestVpcConn1 = "arn:aws:kafka:us-east-1:210987654321:vpc-connection/123456789012/std/ffff-1"
	kafkaTestVpcConn2 = "arn:aws:kafka:us-east-1:111111111111:vpc-connection/123456789012/std/ffff-2"
	kafkaTestClientA1 = kafkaTestClusterA + "/client-vpc-connection/" + kafkaTestVpcConn1
	kafkaTestClientA2 = kafkaTestClusterA + "/client-vpc-connection/" + kafkaTestVpcConn2

	kafkaTestNodeA1 = "arn:aws:kafka:us-east-1:123456789012:broker/std/aaaaaaaa-1111-2222-3333-444444444444-1/1"
	kafkaTestNodeA2 = "arn:aws:kafka:us-east-1:123456789012:broker/std/aaaaaaaa-1111-2222-3333-444444444444-1/2"
	kafkaTestNodeX1 = "arn:aws:kafka:us-east-1:123456789012:broker/exp/bbbbbbbb-1111-2222-3333-444444444444-1/1"
)

var (
	kafkaDenied     = apiErr("ForbiddenException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: kafka:List")
	kafkaGone       = apiErr("NotFoundException", "cluster not found")
	kafkaBadRequest = &kafkatypes.BadRequestException{Message: sdkaws.String("operation not supported for this cluster")}
	// kafkaBadLimit rejects a page size disco chose, which is disco's bug.
	kafkaBadLimit = &kafkatypes.BadRequestException{Message: sdkaws.String("maxResults must be at most 100"), InvalidParameter: sdkaws.String("maxResults")}
	// kafkaBadLimitCased names the same input in another case.
	kafkaBadLimitCased = &kafkatypes.BadRequestException{Message: sdkaws.String("invalid page size"), InvalidParameter: sdkaws.String("MaxResults")}
	// kafkaBadToken rejects a page token disco passed on.
	kafkaBadToken  = &kafkatypes.BadRequestException{Message: sdkaws.String("invalid token"), InvalidParameter: sdkaws.String("nextToken")}
	kafkaThrottled = apiErr("TooManyRequestsException", "slow down")
	// kafkaUnexpected is an error code the model does not list for these ops.
	kafkaUnexpected = apiErr("ConflictException", "unexpected")
	kafkaCreated    = time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
)

// kafkaHandlers answers every MSK op the scan calls. The region has a standard
// provisioned cluster A and an Express cluster X on the first cluster page and
// a serverless cluster S on the second, and
// one configuration; the other older phases are empty. Children: A has two
// pages of topics (one element without an ARN), two pages of client VPC
// connections and two pages of nodes (one without an ARN); X has one topic,
// two pages of channels and one node; S has none. Every child call is recorded
// in calls under its op, keyed by cluster ARN, and fails unless MaxResults is
// the model's cap. A call for a cluster the op should not reach fails.
func kafkaHandlers(calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	for _, op := range []string{"ListTopics", "ListChannels", "DescribeChannel", "ListClientVpcConnections", "ListNodes", "GetClusterPolicy", "ListScramSecrets"} {
		calls[op] = &parentCalls{}
	}
	badLimit := kafkaBadLimit
	return map[string]func(in any) (any, error){
		"ListClustersV2": func(in any) (any, error) {
			if in.(*kafka.ListClustersV2Input).NextToken != nil {
				return &kafka.ListClustersV2Output{ClusterInfoList: []kafkatypes.Cluster{{
					ClusterArn: sdkaws.String(kafkaTestClusterS), ClusterName: sdkaws.String("srv"), ClusterType: kafkatypes.ClusterTypeServerless, State: kafkatypes.ClusterStateActive,
					Serverless: &kafkatypes.Serverless{},
				}}}, nil
			}
			return &kafka.ListClustersV2Output{NextToken: sdkaws.String("page2"), ClusterInfoList: []kafkatypes.Cluster{
				{
					ClusterArn: sdkaws.String(kafkaTestClusterA), ClusterName: sdkaws.String("std"), ClusterType: kafkatypes.ClusterTypeProvisioned, State: kafkatypes.ClusterStateActive,
					Provisioned: &kafkatypes.Provisioned{BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{InstanceType: sdkaws.String("kafka.m5.large")}},
				},
				{
					ClusterArn: sdkaws.String(kafkaTestClusterX), ClusterName: sdkaws.String("exp"), ClusterType: kafkatypes.ClusterTypeProvisioned, State: kafkatypes.ClusterStateActive,
					Provisioned: &kafkatypes.Provisioned{BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{InstanceType: sdkaws.String("express.m7g.large")}},
				},
			}}, nil
		},
		"ListConfigurations": func(any) (any, error) {
			return &kafka.ListConfigurationsOutput{Configurations: []kafkatypes.Configuration{
				{Arn: sdkaws.String("arn:aws:kafka:us-east-1:123456789012:configuration/cfg/1111"), Name: sdkaws.String("cfg")},
			}}, nil
		},
		"ListReplicators":    func(any) (any, error) { return &kafka.ListReplicatorsOutput{}, nil },
		"ListVpcConnections": func(any) (any, error) { return &kafka.ListVpcConnectionsOutput{}, nil },
		"GetClusterPolicy": func(in any) (any, error) {
			calls["GetClusterPolicy"].add(sdkaws.ToString(in.(*kafka.GetClusterPolicyInput).ClusterArn))
			return nil, kafkaGone
		},
		"ListScramSecrets": func(in any) (any, error) {
			calls["ListScramSecrets"].add(sdkaws.ToString(in.(*kafka.ListScramSecretsInput).ClusterArn))
			return &kafka.ListScramSecretsOutput{}, nil
		},
		"ListTopics": func(in any) (any, error) {
			p := in.(*kafka.ListTopicsInput)
			calls["ListTopics"].add(sdkaws.ToString(p.ClusterArn))
			if sdkaws.ToInt32(p.MaxResults) != kafkaPageLimit {
				return nil, badLimit
			}
			topic := func(arn, name string) kafkatypes.TopicInfo {
				return kafkatypes.TopicInfo{
					TopicArn: sdkaws.String(arn), TopicName: sdkaws.String(name),
					PartitionCount: sdkaws.Int32(6), ReplicationFactor: sdkaws.Int32(3), OutOfSyncReplicaCount: sdkaws.Int32(2),
				}
			}
			var out kafka.ListTopicsOutput
			switch sdkaws.ToString(p.ClusterArn) {
			case kafkaTestClusterA:
				if p.NextToken == nil {
					out.Topics = []kafkatypes.TopicInfo{topic(kafkaTestTopicA1, "orders.v1"), {TopicName: sdkaws.String("no-arn")}}
					out.NextToken = sdkaws.String("page2")
				} else {
					out.Topics = []kafkatypes.TopicInfo{topic(kafkaTestTopicA2, "payments_v2")}
				}
			case kafkaTestClusterX:
				out.Topics = []kafkatypes.TopicInfo{topic(kafkaTestTopicX1, "clicks"), topic(kafkaTestTopicXSys, "__consumer_offsets")}
			case kafkaTestClusterS:
			default:
				return nil, kafkaUnexpected
			}
			return &out, nil
		},
		"ListChannels": func(in any) (any, error) {
			p := in.(*kafka.ListChannelsInput)
			calls["ListChannels"].add(sdkaws.ToString(p.ClusterArn))
			if sdkaws.ToInt32(p.MaxResults) != kafkaPageLimit {
				return nil, badLimit
			}
			if sdkaws.ToString(p.ClusterArn) != kafkaTestClusterX {
				return nil, kafkaUnexpected
			}
			if p.NextToken == nil {
				return &kafka.ListChannelsOutput{
					Channels:  []kafkatypes.ChannelInfo{{ChannelArn: sdkaws.String(kafkaTestChannel1)}, {ChannelName: sdkaws.String("no-arn")}},
					NextToken: sdkaws.String("page2"),
				}, nil
			}
			return &kafka.ListChannelsOutput{Channels: []kafkatypes.ChannelInfo{{ChannelArn: sdkaws.String(kafkaTestChannel2)}}}, nil
		},
		"DescribeChannel": func(in any) (any, error) {
			p := in.(*kafka.DescribeChannelInput)
			arn := sdkaws.ToString(p.ChannelArn)
			calls["DescribeChannel"].add(arn)
			if sdkaws.ToString(p.ClusterArn) != kafkaTestClusterX {
				return nil, kafkaUnexpected
			}
			out := &kafka.DescribeChannelOutput{
				ChannelArn: p.ChannelArn, CreationTime: &kafkaCreated, DestinationType: kafkatypes.ChannelDestinationTypeS3,
				TopicConfigurationList: []kafkatypes.TopicConfiguration{{TopicArn: sdkaws.String(kafkaTestTopicX1)}},
			}
			switch arn {
			case kafkaTestChannel1:
				out.ChannelName, out.Status, out.Tags = sdkaws.String("to-s3"), kafkatypes.ChannelStatusActive, map[string]string{"team": "data"}
				out.S3DestinationConfiguration = &kafkatypes.S3DestinationConfiguration{
					ServiceExecutionRoleArn: sdkaws.String("arn:aws:iam::123456789012:role/msk-delivery"),
					Storage:                 &kafkatypes.S3Storage{BucketArn: sdkaws.String("arn:aws:s3:::lake")},
				}
			case kafkaTestChannel2:
				out.ChannelName, out.Status = sdkaws.String("to-ice"), kafkatypes.ChannelStatusCreating
			default:
				return nil, kafkaUnexpected
			}
			return out, nil
		},
		"ListClientVpcConnections": func(in any) (any, error) {
			p := in.(*kafka.ListClientVpcConnectionsInput)
			calls["ListClientVpcConnections"].add(sdkaws.ToString(p.ClusterArn))
			if sdkaws.ToInt32(p.MaxResults) != kafkaPageLimit {
				return nil, badLimit
			}
			conn := func(arn, owner string) kafkatypes.ClientVpcConnection {
				return kafkatypes.ClientVpcConnection{
					VpcConnectionArn: sdkaws.String(arn), Owner: sdkaws.String(owner), State: kafkatypes.VpcConnectionStateAvailable,
					CreationTime: &kafkaCreated, Authentication: sdkaws.String("SASL_IAM"),
				}
			}
			var out kafka.ListClientVpcConnectionsOutput
			switch sdkaws.ToString(p.ClusterArn) {
			case kafkaTestClusterA:
				if p.NextToken == nil {
					out.ClientVpcConnections = []kafkatypes.ClientVpcConnection{conn(kafkaTestVpcConn1, "210987654321"), {Owner: sdkaws.String("no-arn")}}
					out.NextToken = sdkaws.String("page2")
				} else {
					out.ClientVpcConnections = []kafkatypes.ClientVpcConnection{conn(kafkaTestVpcConn2, "111111111111")}
				}
			case kafkaTestClusterX, kafkaTestClusterS:
			default:
				return nil, kafkaUnexpected
			}
			return &out, nil
		},
		"ListNodes": func(in any) (any, error) {
			p := in.(*kafka.ListNodesInput)
			calls["ListNodes"].add(sdkaws.ToString(p.ClusterArn))
			if sdkaws.ToInt32(p.MaxResults) != kafkaPageLimit {
				return nil, badLimit
			}
			node := func(arn string, id float64) kafkatypes.NodeInfo {
				return kafkatypes.NodeInfo{
					NodeARN: sdkaws.String(arn), NodeType: kafkatypes.NodeTypeBroker, InstanceType: sdkaws.String("kafka.m5.large"),
					BrokerNodeInfo: &kafkatypes.BrokerNodeInfo{BrokerId: sdkaws.Float64(id), ClientSubnet: sdkaws.String("subnet-0a"), AttachedENIId: sdkaws.String("eni-0a")},
				}
			}
			var out kafka.ListNodesOutput
			switch sdkaws.ToString(p.ClusterArn) {
			case kafkaTestClusterA:
				if p.NextToken == nil {
					out.NodeInfoList = []kafkatypes.NodeInfo{node(kafkaTestNodeA1, 1)}
					out.NextToken = sdkaws.String("page2")
				} else {
					out.NodeInfoList = []kafkatypes.NodeInfo{node(kafkaTestNodeA2, 2), {NodeType: kafkatypes.NodeTypeBroker}}
				}
			case kafkaTestClusterX:
				out.NodeInfoList = []kafkatypes.NodeInfo{node(kafkaTestNodeX1, 1)}
			default:
				return nil, kafkaUnexpected
			}
			return &out, nil
		},
	}
}

func scanKafkaTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, kafkaDenied)
	total, _, err := scanKafka(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

var kafkaAllChildRows = map[string][]string{
	TypeMSKTopic:               {kafkaTestTopicXSys, kafkaTestTopicX1, kafkaTestTopicA1, kafkaTestTopicA2},
	TypeMSKChannel:             {kafkaTestChannel2, kafkaTestChannel1},
	TypeMSKClientVpcConnection: {kafkaTestClientA2, kafkaTestClientA1},
	TypeMSKNode:                {kafkaTestNodeX1, kafkaTestNodeA1, kafkaTestNodeA2},
}

func kafkaChildRowsWithout(typ string, drop ...string) map[string][]string {
	out := map[string][]string{}
	for k, v := range kafkaAllChildRows {
		out[k] = v
	}
	out[typ] = slices.DeleteFunc(slices.Clone(out[typ]), func(id string) bool { return slices.Contains(drop, id) })
	return out
}

func assertKafkaChildRows(t *testing.T, st *store.Store, want map[string][]string) {
	t.Helper()
	for typ, ids := range want {
		if got := nativeIDs(rowsByNativeID(t, st, typ)); !slices.Equal(got, ids) {
			t.Errorf("%s rows = %v, want %v", typ, got, ids)
		}
	}
}

func TestScanKafka_StoresClusterChildren(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	total, err := scanKafkaTopLevel(t, st, kafkaHandlers(calls))
	if err != nil {
		t.Fatalf("scanKafka: %v", err)
	}
	// 3 clusters + 1 configuration + 4 topics + 2 channels + 2 client VPC connections + 3 nodes
	if total != 15 {
		t.Errorf("total = %d, want 15", total)
	}
	topics := rowsByNativeID(t, st, TypeMSKTopic)
	if !topics[kafkaTestTopicXSys].ManagedByProvider || topics[kafkaTestTopicX1].ManagedByProvider {
		t.Errorf("ManagedByProvider: __consumer_offsets = %v, clicks = %v; want true, false",
			topics[kafkaTestTopicXSys].ManagedByProvider, topics[kafkaTestTopicX1].ManagedByProvider)
	}
	assertKafkaChildRows(t, st, kafkaAllChildRows)

	all := []string{kafkaTestClusterA, kafkaTestClusterX, kafkaTestClusterS}
	slices.Sort(all)
	for op, want := range map[string][]string{
		"ListTopics":               all,
		"ListClientVpcConnections": all,
		"ListChannels":             {kafkaTestClusterX},
		"ListNodes":                {kafkaTestClusterX, kafkaTestClusterA},
		"DescribeChannel":          {kafkaTestChannel2, kafkaTestChannel1},
	} {
		if got := calls[op].distinct(); !slices.Equal(got, want) {
			t.Errorf("%s calls = %v, want %v", op, got, want)
		}
	}

	created := kafkaCreated.Format(time.RFC3339)
	for _, tc := range []struct {
		typ, nativeID, name, status, createdAt, attrKey string
		attrVal                                         any
	}{
		{TypeMSKTopic, kafkaTestTopicA2, "payments_v2", "", "", "PartitionCount", 6.0},
		{TypeMSKChannel, kafkaTestChannel1, "to-s3", "ACTIVE", created, "DestinationType", "S3"},
		{TypeMSKClientVpcConnection, kafkaTestClientA2, "", "AVAILABLE", created, "Owner", "111111111111"},
		{TypeMSKNode, kafkaTestNodeX1, "", "", "", "NodeType", "BROKER"},
	} {
		r := rowsByNativeID(t, st, tc.typ)[tc.nativeID]
		if r.Type != tc.typ || sv(r.Region) != testRegion || sv(r.Name) != tc.name || sv(r.Status) != tc.status || sv(r.CreatedAt) != tc.createdAt {
			t.Errorf("%s row = type %q region %q name %q status %q created %q", tc.nativeID, r.Type, sv(r.Region), sv(r.Name), sv(r.Status), sv(r.CreatedAt))
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("attrs %s: %v", tc.nativeID, err)
		}
		if attrs[tc.attrKey] != tc.attrVal {
			t.Errorf("%s attrs[%s] = %v, want %v (SDK element verbatim)", tc.nativeID, tc.attrKey, attrs[tc.attrKey], tc.attrVal)
		}
	}
	if ch := rowsByNativeID(t, st, TypeMSKChannel)[kafkaTestChannel1]; sv(ch.TagsJSON) != `{"team":"data"}` {
		t.Errorf("channel tags = %q, want the DescribeChannel tags", sv(ch.TagsJSON))
	}

	for parent, children := range map[string][]string{
		kafkaTestClusterA: {kafkaTestTopicA1, kafkaTestTopicA2, kafkaTestClientA1, kafkaTestClientA2, kafkaTestNodeA1, kafkaTestNodeA2},
		kafkaTestClusterX: {kafkaTestTopicX1, kafkaTestTopicXSys, kafkaTestChannel1, kafkaTestChannel2, kafkaTestNodeX1},
	} {
		for _, child := range children {
			assertContainsNative(t, st, parent, child)
		}
	}
}

func TestScanKafka_NoChildren(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := kafkaHandlers(calls)
	h["ListTopics"] = func(any) (any, error) { return &kafka.ListTopicsOutput{}, nil }
	h["ListChannels"] = func(any) (any, error) { return &kafka.ListChannelsOutput{}, nil }
	h["ListClientVpcConnections"] = func(any) (any, error) { return &kafka.ListClientVpcConnectionsOutput{}, nil }
	h["ListNodes"] = func(any) (any, error) { return &kafka.ListNodesOutput{}, nil }
	total, err := scanKafkaTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanKafka: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want the 3 clusters and 1 configuration only", total)
	}
	for typ := range kafkaAllChildRows {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
	if n := len(calls["DescribeChannel"].ids); n != 0 {
		t.Errorf("DescribeChannel called %d times with no channels", n)
	}
}

// A cluster (or, for DescribeChannel, a channel) deleted since listing,
// denied, failing transiently or rejected with BadRequestException drops only
// its own children: NotFoundException is skipped silently for the ops whose
// model lists it (ListChannels, DescribeChannel, ListNodes), and each of the
// others warns once however many parents hit it. NotFoundException from an op
// whose model does not list it (ListTopics, ListClientVpcConnections) fails
// the phase. A BadRequestException naming maxResults or nextToken (any case)
// is disco's own input and is returned, as is any other error. Every other
// phase, older ones included, still stores its rows.
func TestScanKafka_FailedChildParent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		op        string           // the op whose calls fail
		opLabel   string           // the label it warns under
		fail      map[string]error // cluster or channel ARN -> error
		wantCode  string           // "" means the scan returns nil
		wantRows  map[string][]string
		wantWarns int
	}{
		{
			"topics denied", "ListTopics", kafkaListTopicsOp,
			map[string]error{kafkaTestClusterA: kafkaDenied, kafkaTestClusterX: kafkaDenied},
			"", kafkaChildRowsWithout(TypeMSKTopic, kafkaAllChildRows[TypeMSKTopic]...), 1,
		},
		{
			"topics throttled", "ListTopics", kafkaListTopicsOp,
			map[string]error{kafkaTestClusterX: kafkaThrottled},
			"", kafkaChildRowsWithout(TypeMSKTopic, kafkaTestTopicX1, kafkaTestTopicXSys), 1,
		},
		{
			"topics rejected", "ListTopics", kafkaListTopicsOp,
			map[string]error{kafkaTestClusterA: kafkaBadRequest, kafkaTestClusterX: kafkaBadRequest},
			"", kafkaChildRowsWithout(TypeMSKTopic, kafkaAllChildRows[TypeMSKTopic]...), 1,
		},
		{
			"topics not found is unmodelled", "ListTopics", kafkaListTopicsOp,
			map[string]error{kafkaTestClusterA: kafkaGone},
			"NotFoundException", kafkaChildRowsWithout(TypeMSKTopic, kafkaAllChildRows[TypeMSKTopic]...), 0,
		},
		{
			"topics page size rejected", "ListTopics", kafkaListTopicsOp,
			map[string]error{kafkaTestClusterS: kafkaBadLimit},
			"BadRequestException", kafkaChildRowsWithout(TypeMSKTopic, kafkaAllChildRows[TypeMSKTopic]...), 0,
		},
		{
			"topics other", "ListTopics", kafkaListTopicsOp,
			map[string]error{kafkaTestClusterS: kafkaUnexpected},
			"ConflictException", kafkaChildRowsWithout(TypeMSKTopic, kafkaAllChildRows[TypeMSKTopic]...), 0,
		},
		{
			"channels gone", "ListChannels", kafkaListChannelsOp,
			map[string]error{kafkaTestClusterX: kafkaGone},
			"", kafkaChildRowsWithout(TypeMSKChannel, kafkaAllChildRows[TypeMSKChannel]...), 0,
		},
		{
			"channels denied", "ListChannels", kafkaListChannelsOp,
			map[string]error{kafkaTestClusterX: kafkaDenied},
			"", kafkaChildRowsWithout(TypeMSKChannel, kafkaAllChildRows[TypeMSKChannel]...), 1,
		},
		{
			"channels throttled", "ListChannels", kafkaListChannelsOp,
			map[string]error{kafkaTestClusterX: kafkaThrottled},
			"", kafkaChildRowsWithout(TypeMSKChannel, kafkaAllChildRows[TypeMSKChannel]...), 1,
		},
		{
			"channels rejected", "ListChannels", kafkaListChannelsOp,
			map[string]error{kafkaTestClusterX: kafkaBadRequest},
			"", kafkaChildRowsWithout(TypeMSKChannel, kafkaAllChildRows[TypeMSKChannel]...), 1,
		},
		{
			"channels other", "ListChannels", kafkaListChannelsOp,
			map[string]error{kafkaTestClusterX: kafkaUnexpected},
			"ConflictException", kafkaChildRowsWithout(TypeMSKChannel, kafkaAllChildRows[TypeMSKChannel]...), 0,
		},
		{
			"channel deleted before describe", "DescribeChannel", kafkaDescribeChannelOp,
			map[string]error{kafkaTestChannel1: kafkaGone},
			"", kafkaChildRowsWithout(TypeMSKChannel, kafkaTestChannel1), 0,
		},
		{
			"channel describe denied", "DescribeChannel", kafkaDescribeChannelOp,
			map[string]error{kafkaTestChannel1: kafkaDenied, kafkaTestChannel2: kafkaDenied},
			"", kafkaChildRowsWithout(TypeMSKChannel, kafkaAllChildRows[TypeMSKChannel]...), 1,
		},
		{
			"channel describe rejected", "DescribeChannel", kafkaDescribeChannelOp,
			map[string]error{kafkaTestChannel1: kafkaBadRequest},
			"", kafkaChildRowsWithout(TypeMSKChannel, kafkaTestChannel1), 1,
		},
		{
			"one channel describe denied", "DescribeChannel", kafkaDescribeChannelOp,
			map[string]error{kafkaTestChannel1: kafkaDenied},
			"", kafkaChildRowsWithout(TypeMSKChannel, kafkaTestChannel1), 1,
		},
		{
			"channel describe throttled", "DescribeChannel", kafkaListChannelsOp,
			map[string]error{kafkaTestChannel2: kafkaThrottled},
			"", kafkaChildRowsWithout(TypeMSKChannel, kafkaAllChildRows[TypeMSKChannel]...), 1,
		},
		{
			"channel describe other", "DescribeChannel", kafkaDescribeChannelOp,
			map[string]error{kafkaTestChannel2: kafkaUnexpected},
			"ConflictException", kafkaChildRowsWithout(TypeMSKChannel, kafkaAllChildRows[TypeMSKChannel]...), 0,
		},
		{
			"client connections denied", "ListClientVpcConnections", kafkaListClientVpcConnectionsOp,
			map[string]error{kafkaTestClusterA: kafkaDenied, kafkaTestClusterX: kafkaDenied},
			"", kafkaChildRowsWithout(TypeMSKClientVpcConnection, kafkaAllChildRows[TypeMSKClientVpcConnection]...), 1,
		},
		{
			"client connections throttled", "ListClientVpcConnections", kafkaListClientVpcConnectionsOp,
			map[string]error{kafkaTestClusterA: kafkaThrottled},
			"", kafkaChildRowsWithout(TypeMSKClientVpcConnection, kafkaAllChildRows[TypeMSKClientVpcConnection]...), 1,
		},
		{
			"client connections rejected", "ListClientVpcConnections", kafkaListClientVpcConnectionsOp,
			map[string]error{kafkaTestClusterS: kafkaBadRequest},
			"", kafkaAllChildRows, 1,
		},
		{
			"client connections token rejected", "ListClientVpcConnections", kafkaListClientVpcConnectionsOp,
			map[string]error{kafkaTestClusterA: kafkaBadToken},
			"BadRequestException", kafkaChildRowsWithout(TypeMSKClientVpcConnection, kafkaAllChildRows[TypeMSKClientVpcConnection]...), 0,
		},
		{
			"client connections not found is unmodelled", "ListClientVpcConnections", kafkaListClientVpcConnectionsOp,
			map[string]error{kafkaTestClusterA: kafkaGone},
			"NotFoundException", kafkaChildRowsWithout(TypeMSKClientVpcConnection, kafkaAllChildRows[TypeMSKClientVpcConnection]...), 0,
		},
		{
			"client connections other", "ListClientVpcConnections", kafkaListClientVpcConnectionsOp,
			map[string]error{kafkaTestClusterA: kafkaUnexpected},
			"ConflictException", kafkaChildRowsWithout(TypeMSKClientVpcConnection, kafkaAllChildRows[TypeMSKClientVpcConnection]...), 0,
		},
		{
			"nodes gone", "ListNodes", kafkaListNodesOp,
			map[string]error{kafkaTestClusterA: kafkaGone},
			"", kafkaChildRowsWithout(TypeMSKNode, kafkaTestNodeA1, kafkaTestNodeA2), 0,
		},
		{
			"nodes denied", "ListNodes", kafkaListNodesOp,
			map[string]error{kafkaTestClusterA: kafkaDenied, kafkaTestClusterX: kafkaDenied},
			"", kafkaChildRowsWithout(TypeMSKNode, kafkaAllChildRows[TypeMSKNode]...), 1,
		},
		{
			"nodes throttled", "ListNodes", kafkaListNodesOp,
			map[string]error{kafkaTestClusterA: kafkaThrottled, kafkaTestClusterX: kafkaThrottled},
			"", kafkaChildRowsWithout(TypeMSKNode, kafkaAllChildRows[TypeMSKNode]...), 1,
		},
		{
			"nodes rejected", "ListNodes", kafkaListNodesOp,
			map[string]error{kafkaTestClusterX: kafkaBadRequest},
			"", kafkaChildRowsWithout(TypeMSKNode, kafkaTestNodeX1), 1,
		},
		{
			"nodes page size rejected", "ListNodes", kafkaListNodesOp,
			map[string]error{kafkaTestClusterX: kafkaBadLimit},
			"BadRequestException", kafkaChildRowsWithout(TypeMSKNode, kafkaAllChildRows[TypeMSKNode]...), 0,
		},
		{
			"nodes page size rejected, other case", "ListNodes", kafkaListNodesOp,
			map[string]error{kafkaTestClusterA: kafkaBadLimitCased},
			"BadRequestException", kafkaChildRowsWithout(TypeMSKNode, kafkaAllChildRows[TypeMSKNode]...), 0,
		},
		{
			"nodes other", "ListNodes", kafkaListNodesOp,
			map[string]error{kafkaTestClusterX: kafkaUnexpected},
			"ConflictException", kafkaChildRowsWithout(TypeMSKNode, kafkaAllChildRows[TypeMSKNode]...), 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.opLabel)
			calls := map[string]*parentCalls{}
			h := kafkaHandlers(calls)
			next := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				var key string
				switch p := in.(type) {
				case *kafka.ListTopicsInput:
					key = sdkaws.ToString(p.ClusterArn)
				case *kafka.ListChannelsInput:
					key = sdkaws.ToString(p.ClusterArn)
				case *kafka.DescribeChannelInput:
					key = sdkaws.ToString(p.ChannelArn)
				case *kafka.ListClientVpcConnectionsInput:
					key = sdkaws.ToString(p.ClusterArn)
				case *kafka.ListNodesInput:
					key = sdkaws.ToString(p.ClusterArn)
				}
				if err, ok := tc.fail[key]; ok {
					return nil, err
				}
				return next(in)
			}
			_, err := scanKafkaTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			want := tc.wantRows
			if tc.wantCode != "" && tc.op == "ListTopics" {
				// A failed topic phase keeps the pages other clusters stored
				// before the error cancelled them, which depends on scheduling.
				want = kafkaRowsWithoutType(want, TypeMSKTopic)
			}
			assertKafkaChildRows(t, st, want)
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if n := len(rowsByNativeID(t, st, TypeMSKCluster)); n != 3 {
				t.Errorf("cluster rows = %d, want 3", n)
			}
			if n := len(rowsByNativeID(t, st, TypeMSKConfiguration)); n != 1 {
				t.Errorf("configuration rows = %d, want 1", n)
			}
		})
	}
}

// kafkaRowsWithoutType returns want without typ.
func kafkaRowsWithoutType(want map[string][]string, typ string) map[string][]string {
	out := map[string][]string{}
	for k, v := range want {
		if k != typ {
			out[k] = v
		}
	}
	return out
}

// A topic page that fails after an earlier page was stored keeps that page.
func TestScanKafka_TopicPagesStoredAsTheyArrive(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, kafkaListTopicsOp)
	calls := map[string]*parentCalls{}
	h := kafkaHandlers(calls)
	next := h["ListTopics"]
	h["ListTopics"] = func(in any) (any, error) {
		if p := in.(*kafka.ListTopicsInput); p.NextToken != nil {
			return nil, kafkaThrottled
		}
		return next(in)
	}
	total, err := scanKafkaTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanKafka: %v", err)
	}
	if total != 14 {
		t.Errorf("total = %d, want 14 (every row but topic A2, the stored first page counted)", total)
	}
	assertKafkaChildRows(t, st, kafkaChildRowsWithout(TypeMSKTopic, kafkaTestTopicA2))
	assertContainsNative(t, st, kafkaTestClusterA, kafkaTestTopicA1)
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

// The child phases run after every older phase and whatever those return: a
// hard error in an older phase stops the older phases after it, is returned,
// and costs the child phases nothing; a transient one is demoted to a warning.
func TestScanKafka_OlderPhaseErrorKeepsChildren(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
	}{
		{"hard", kafkaUnexpected, "ConflictException"},
		{"transient", kafkaThrottled, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := map[string]*parentCalls{}
			h := kafkaHandlers(calls)
			h["ListReplicators"] = func(any) (any, error) { return nil, tc.err }
			_, err := scanKafkaTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			assertKafkaChildRows(t, st, kafkaAllChildRows)
			if n := len(calls["GetClusterPolicy"].ids); n != 0 {
				t.Errorf("GetClusterPolicy called %d times after the replicator phase failed", n)
			}
			if n := len(rowsByNativeID(t, st, TypeMSKConfiguration)); n != 1 {
				t.Errorf("configuration rows = %d, want the 1 stored before the failure", n)
			}
		})
	}
}

// A failed cluster phase lists no parents, so no per-cluster op is called.
func TestScanKafka_ClusterPhaseErrorSkipsClusterPhases(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := kafkaHandlers(calls)
	h["ListClustersV2"] = func(any) (any, error) { return nil, kafkaUnexpected }
	_, err := scanKafkaTopLevel(t, st, h)
	assertAPICode(t, err, "ConflictException")
	for op, c := range calls {
		if len(c.ids) != 0 {
			t.Errorf("%s called %v with no cluster listed", op, c.ids)
		}
	}
}

// Per-cluster phases fan out over the clusters listed this scan, not a cluster
// row an earlier scan left in the store.
func TestScanKafka_PerClusterPhasesSkipStaleClusters(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeMSKCluster, kafkaTestStale, testRegion, "{}")
	calls := map[string]*parentCalls{}
	if _, err := scanKafkaTopLevel(t, st, kafkaHandlers(calls)); err != nil {
		t.Fatalf("scanKafka: %v", err)
	}
	for _, op := range []string{"GetClusterPolicy", "ListScramSecrets", "ListTopics", "ListClientVpcConnections"} {
		if got := calls[op].distinct(); slices.Contains(got, kafkaTestStale) || len(got) != 3 {
			t.Errorf("%s calls = %v, want the 3 listed clusters only", op, got)
		}
	}
}

// A transient older-phase error is demoted to one warning before the join, so
// a child phase's hard error still classifies the service as failed rather
// than transient.
func TestScanKafka_TransientOlderPhaseWithFailedChildPhaseIsAnError(t *testing.T) {
	for _, op := range []string{"ListTopics", "ListChannels", "DescribeChannel", "ListClientVpcConnections", "ListNodes"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "kafka")
			h := kafkaHandlers(map[string]*parentCalls{})
			h["ListReplicators"] = func(any) (any, error) { return nil, kafkaThrottled }
			h[op] = func(any) (any, error) { return nil, kafkaUnexpected }
			_, err := scanKafkaTopLevel(t, st, h)
			assertAPICode(t, err, "ConflictException")
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warns != 1 {
				t.Errorf("older-phase warnings = %d, want 1", *warns)
			}
		})
	}
}

// A ListClustersV2 failure on a later page still hands the child phases the
// clusters of the pages stored before it, and is returned.
func TestScanKafka_ClusterPageErrorKeepsStoredClusters(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := kafkaHandlers(calls)
	next := h["ListClustersV2"]
	h["ListClustersV2"] = func(in any) (any, error) {
		if in.(*kafka.ListClustersV2Input).NextToken != nil {
			return nil, kafkaUnexpected
		}
		return next(in)
	}
	_, err := scanKafkaTopLevel(t, st, h)
	assertAPICode(t, err, "ConflictException")
	assertKafkaChildRows(t, st, kafkaAllChildRows)
	want := []string{kafkaTestClusterX, kafkaTestClusterA}
	for _, op := range []string{"ListTopics", "ListClientVpcConnections"} {
		if got := calls[op].distinct(); !slices.Equal(got, want) {
			t.Errorf("%s calls = %v, want the page-1 clusters %v", op, got, want)
		}
	}
}

// A failed cluster write may have left the listed clusters unstored, so no
// per-cluster phase runs for them.
func TestScanKafka_ClusterWriteErrorRunsNoClusterPhase(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	calls := map[string]*parentCalls{}
	_, err = scanKafkaTopLevel(t, st, kafkaHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	for op, c := range calls {
		if len(c.ids) != 0 {
			t.Errorf("%s called for %v after the cluster write failed", op, c.ids)
		}
	}
}

// kafkaTestClusterX2 is a second Express cluster.
const kafkaTestClusterX2 = "arn:aws:kafka:us-east-1:123456789012:cluster/exp2/eeeeeeee-1111-2222-3333-444444444444-1"

// kafkaTwoExpressHandlers lists clusters X and X2, both Express, each with one
// channel and no other children.
func kafkaTwoExpressHandlers(calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	h := kafkaHandlers(calls)
	express := func(arn string) kafkatypes.Cluster {
		return kafkatypes.Cluster{
			ClusterArn: sdkaws.String(arn), ClusterType: kafkatypes.ClusterTypeProvisioned, State: kafkatypes.ClusterStateActive,
			Provisioned: &kafkatypes.Provisioned{BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{InstanceType: sdkaws.String("express.m7g.large")}},
		}
	}
	h["ListClustersV2"] = func(any) (any, error) {
		return &kafka.ListClustersV2Output{ClusterInfoList: []kafkatypes.Cluster{express(kafkaTestClusterX), express(kafkaTestClusterX2)}}, nil
	}
	h["ListTopics"] = func(any) (any, error) { return &kafka.ListTopicsOutput{}, nil }
	h["ListClientVpcConnections"] = func(any) (any, error) { return &kafka.ListClientVpcConnectionsOutput{}, nil }
	h["ListNodes"] = func(any) (any, error) { return &kafka.ListNodesOutput{}, nil }
	h["ListChannels"] = func(in any) (any, error) {
		p := in.(*kafka.ListChannelsInput)
		calls["ListChannels"].add(sdkaws.ToString(p.ClusterArn))
		return &kafka.ListChannelsOutput{Channels: []kafkatypes.ChannelInfo{{ChannelArn: sdkaws.String(sdkaws.ToString(p.ClusterArn) + "/channel")}}}, nil
	}
	return h
}

// A DescribeChannel denial on every Express cluster warns once in all.
func TestScanKafka_DescribeChannelDeniedWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, kafkaDescribeChannelOp)
	calls := map[string]*parentCalls{}
	h := kafkaTwoExpressHandlers(calls)
	h["DescribeChannel"] = func(in any) (any, error) {
		calls["DescribeChannel"].add(sdkaws.ToString(in.(*kafka.DescribeChannelInput).ClusterArn))
		return nil, kafkaDenied
	}
	if _, err := scanKafkaTopLevel(t, st, h); err != nil {
		t.Fatalf("scanKafka: %v", err)
	}
	if got := calls["DescribeChannel"].distinct(); len(got) != 2 {
		t.Errorf("DescribeChannel clusters = %v, want both Express clusters denied", got)
	}
	if *warns != 1 {
		t.Errorf("DescribeChannel warnings = %d, want 1", *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeMSKChannel)); n != 0 {
		t.Errorf("channel rows = %d, want 0", n)
	}
}

// ListChannels has no paginator: a token the service repeats ends the listing
// with the channels read so far rather than looping.
func TestScanKafka_ChannelsRepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := kafkaHandlers(calls)
	var listed int
	h["ListChannels"] = func(in any) (any, error) {
		p := in.(*kafka.ListChannelsInput)
		calls["ListChannels"].add(sdkaws.ToString(p.ClusterArn))
		listed++
		switch {
		case listed > 3:
			return nil, kafkaUnexpected // a stop that did not happen
		case p.NextToken == nil:
			return &kafka.ListChannelsOutput{Channels: []kafkatypes.ChannelInfo{{ChannelArn: sdkaws.String(kafkaTestChannel1)}}, NextToken: sdkaws.String("same")}, nil
		default:
			return &kafka.ListChannelsOutput{Channels: []kafkatypes.ChannelInfo{{ChannelArn: sdkaws.String(kafkaTestChannel2)}}, NextToken: sdkaws.String("same")}, nil
		}
	}
	if _, err := scanKafkaTopLevel(t, st, h); err != nil {
		t.Fatalf("scanKafka: %v", err)
	}
	if listed != 2 {
		t.Errorf("ListChannels calls = %d, want 2", listed)
	}
	assertKafkaChildRows(t, st, kafkaAllChildRows)
}
