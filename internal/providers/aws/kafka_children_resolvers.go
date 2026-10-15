package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		noopMSKClusterChildren,
		EdgeDecl{TypeMSKCluster, TypeMSKTopic, store.RelContains},
		EdgeDecl{TypeMSKCluster, TypeMSKChannel, store.RelContains},
		EdgeDecl{TypeMSKCluster, TypeMSKClientVpcConnection, store.RelContains},
		EdgeDecl{TypeMSKCluster, TypeMSKNode, store.RelContains},
	)
	registerResolver(
		resolveMSKNodeRefs,
		EdgeDecl{TypeMSKNode, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeMSKNode, TypeEC2NetworkInterface, store.RelAttachedTo},
	)
	registerResolver(
		resolveMSKChannelRefs,
		EdgeDecl{TypeMSKChannel, TypeMSKTopic, store.RelUses},
		EdgeDecl{TypeMSKChannel, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeMSKChannel, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeMSKChannel, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveMSKClientVpcConnectionRefs,
		EdgeDecl{TypeMSKClientVpcConnection, TypeMSKVpcConnection, store.RelAttachedTo},
	)
}

// noopMSKClusterChildren only carries the EdgeDecl metadata: the cluster child
// phases record the cluster → child contains pairs themselves.
func noopMSKClusterChildren(_ *account, _ *store.Store) error {
	return nil
}

// listMSKRows returns acct's rows of typ.
func listMSKRows(acct *account, st *store.Store, typ string) ([]store.Resource, error) {
	return st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{typ}, Limit: util.AllResources,
	})
}

// emitMSKEdge upserts src → the row whose NativeID is tgtNativeID when that
// row is in set.
func emitMSKEdge(st *store.Store, acct *account, srcID, tgtNativeID, kind string, set map[string]bool) error {
	if tgtNativeID == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, tgtNativeID)
	if !set[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(srcID, tgtID, kind, "directed", nil); err != nil {
		return fmt.Errorf("upsert msk %s → %s: %w", srcID, tgtID, err)
	}
	return nil
}

// mskNodeENIAttrs is the part of a node's ENI-bearing sub-structs it reads.
type mskNodeENIAttrs struct {
	AttachedENIID *string `json:"AttachedENIId"`
}

// resolveMSKNodeRefs wires each broker node to its client subnet and the
// ENI attached to it (a ZooKeeper node carries an ENI too).
func resolveMSKNodeRefs(acct *account, st *store.Store) error {
	rows, err := listMSKRows(acct, st, TypeMSKNode)
	if err != nil || len(rows) == 0 {
		return err
	}
	subnets, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	enis, err := scannedIDSet(acct, st, TypeEC2NetworkInterface)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			BrokerNodeInfo *struct {
				mskNodeENIAttrs
				ClientSubnet *string `json:"ClientSubnet"`
			} `json:"BrokerNodeInfo"`
			ZookeeperNodeInfo *mskNodeENIAttrs `json:"ZookeeperNodeInfo"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode msk node %s: %w", r.NativeID, err)
		}
		region := sv(r.Region)
		var eniIDs []string
		if b := attrs.BrokerNodeInfo; b != nil {
			if s := sv(b.ClientSubnet); s != "" {
				if err := emitMSKEdge(st, acct, r.ID, ec2ARN(region, acct.ID, "subnet", s), store.RelAttachedTo, subnets); err != nil {
					return err
				}
			}
			eniIDs = append(eniIDs, sv(b.AttachedENIID))
		}
		if z := attrs.ZookeeperNodeInfo; z != nil {
			eniIDs = append(eniIDs, sv(z.AttachedENIID))
		}
		for _, eni := range eniIDs {
			if eni == "" {
				continue
			}
			if err := emitMSKEdge(st, acct, r.ID, ec2ARN(region, acct.ID, "network-interface", eni), store.RelAttachedTo, enis); err != nil {
				return err
			}
		}
	}
	return nil
}

// mskChannelDLQAttrs is a channel destination's dead-letter bucket.
type mskChannelDLQAttrs struct {
	DeadLetterQueueS3 *struct {
		BucketArn *string `json:"BucketArn"`
	} `json:"DeadLetterQueueS3"`
	ServiceExecutionRoleArn *string `json:"ServiceExecutionRoleArn"`
}

// mskChannelAttrs is the part of a DescribeChannel body resolveMSKChannelRefs
// reads.
type mskChannelAttrs struct {
	TopicConfigurationList []struct {
		TopicArn *string `json:"TopicArn"`
	} `json:"TopicConfigurationList"`
	EncryptionConfiguration *struct {
		KmsKeyArn *string `json:"KmsKeyArn"`
	} `json:"EncryptionConfiguration"`
	S3DestinationConfiguration *struct {
		mskChannelDLQAttrs
		Storage *struct {
			BucketArn *string `json:"BucketArn"`
		} `json:"Storage"`
	} `json:"S3DestinationConfiguration"`
	IcebergDestinationConfiguration *mskChannelDLQAttrs `json:"IcebergDestinationConfiguration"`
}

// mskChannelTargets are the scanned id sets a channel's refs resolve against.
type mskChannelTargets struct {
	topics, buckets, roles map[string]bool
	kms                    *kmsResolveIndex
}

// resolveMSKChannelRefs wires each channel to the topics it reads, the S3
// buckets it delivers and dead-letters to, the IAM role MSK assumes for its
// destination, and its KMS key.
func resolveMSKChannelRefs(acct *account, st *store.Store) error {
	rows, err := listMSKRows(acct, st, TypeMSKChannel)
	if err != nil || len(rows) == 0 {
		return err
	}
	var tg mskChannelTargets
	if tg.topics, err = scannedIDSet(acct, st, TypeMSKTopic); err != nil {
		return err
	}
	if tg.buckets, err = scannedIDSet(acct, st, TypeS3Bucket); err != nil {
		return err
	}
	if tg.roles, err = scannedIDSet(acct, st, TypeIAMRole); err != nil {
		return err
	}
	if tg.kms, err = loadKMSResolveIndex(acct, st); err != nil {
		return err
	}
	for _, r := range rows {
		if err := resolveMSKChannel(acct, st, r, &tg); err != nil {
			return err
		}
	}
	return nil
}

func resolveMSKChannel(acct *account, st *store.Store, r store.Resource, tg *mskChannelTargets) error {
	var attrs mskChannelAttrs
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		return fmt.Errorf("decode msk channel %s: %w", r.NativeID, err)
	}
	for _, tc := range attrs.TopicConfigurationList {
		if err := emitMSKEdge(st, acct, r.ID, sv(tc.TopicArn), store.RelUses, tg.topics); err != nil {
			return err
		}
	}
	var buckets []string
	var dests []*mskChannelDLQAttrs
	if s3 := attrs.S3DestinationConfiguration; s3 != nil {
		if s3.Storage != nil {
			buckets = append(buckets, sv(s3.Storage.BucketArn))
		}
		dests = append(dests, &s3.mskChannelDLQAttrs)
	}
	if ice := attrs.IcebergDestinationConfiguration; ice != nil {
		dests = append(dests, ice)
	}
	for _, d := range dests {
		if d.DeadLetterQueueS3 != nil {
			buckets = append(buckets, sv(d.DeadLetterQueueS3.BucketArn))
		}
		if err := emitMSKEdge(st, acct, r.ID, sv(d.ServiceExecutionRoleArn), store.RelUses, tg.roles); err != nil {
			return err
		}
	}
	for _, b := range buckets {
		if err := emitMSKEdge(st, acct, r.ID, b, store.RelUses, tg.buckets); err != nil {
			return err
		}
	}
	if enc := attrs.EncryptionConfiguration; enc != nil {
		if keyID, ok := tg.kms.resolveKMSKeyID(sv(enc.KmsKeyArn), sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert msk channel → kms key: %w", err)
			}
		}
	}
	return nil
}

// resolveMSKClientVpcConnectionRefs wires the cluster owner's record of a
// client VPC connection to the client's own aws:kafka:vpc-connection row. Only
// a connection whose client is this account can resolve: the client row lives
// under the client account (the ARN's account), and resolvers run per account.
func resolveMSKClientVpcConnectionRefs(acct *account, st *store.Store) error {
	rows, err := listMSKRows(acct, st, TypeMSKClientVpcConnection)
	if err != nil || len(rows) == 0 {
		return err
	}
	conns, err := scannedIDSet(acct, st, TypeMSKVpcConnection)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcConnectionArn *string `json:"VpcConnectionArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode msk client vpc connection %s: %w", r.NativeID, err)
		}
		if err := emitMSKEdge(st, acct, r.ID, sv(attrs.VpcConnectionArn), store.RelAttachedTo, conns); err != nil {
			return err
		}
	}
	return nil
}
