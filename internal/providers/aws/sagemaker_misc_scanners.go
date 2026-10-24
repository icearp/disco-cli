package aws

import (
	"context"
	"fmt"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerCluster, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerWorkteam, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerClusterNode, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerSubscribedWorkteam, Service: "sagemaker"})
}

// sagemakerMiscAPI is the narrow surface for the misc family —
// resources that don't fit other groupings (HyperPod cluster and its nodes,
// ground-truth workteams).
type sagemakerMiscAPI interface {
	sagemakerTagsAPI
	ListClusters(context.Context, *sagemaker.ListClustersInput, ...func(*sagemaker.Options)) (*sagemaker.ListClustersOutput, error)
	DescribeCluster(context.Context, *sagemaker.DescribeClusterInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeClusterOutput, error)
	ListWorkteams(context.Context, *sagemaker.ListWorkteamsInput, ...func(*sagemaker.Options)) (*sagemaker.ListWorkteamsOutput, error)
	DescribeWorkteam(context.Context, *sagemaker.DescribeWorkteamInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeWorkteamOutput, error)
	ListClusterNodes(context.Context, *sagemaker.ListClusterNodesInput, ...func(*sagemaker.Options)) (*sagemaker.ListClusterNodesOutput, error)
	ListSubscribedWorkteams(context.Context, *sagemaker.ListSubscribedWorkteamsInput, ...func(*sagemaker.Options)) (*sagemaker.ListSubscribedWorkteamsOutput, error)
}

// scanSageMakerMisc runs the misc-family phases: HyperPod clusters, their
// nodes, and ground-truth workteams. The node phase fans out per scanned
// cluster, so it runs after clusters.
func scanSageMakerMisc(ctx context.Context, client sagemakerMiscAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListClusters", func() (int, int, error) { return scanSageMakerClusters(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListWorkteams", func() (int, int, error) { return scanSageMakerWorkteams(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListSubscribedWorkteams", func() (int, int, error) {
			return scanSageMakerSubscribedWorkteams(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListClusterNodes", func() (int, int, error) { return scanSageMakerClusterNodes(ctx, client, acct, region, st, scanID) }},
	)
}

func scanSageMakerClusters(ctx context.Context, client sagemakerMiscAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.ClusterSummary]{
		listOp: "sagemaker:ListClusters", describeOp: "sagemaker:DescribeCluster", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.ClusterSummary, *string, error) {
			out, err := client.ListClusters(ctx, &sagemaker.ListClustersInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ClusterSummaries, out.NextToken, nil
		},
		name: func(s smtypes.ClusterSummary) string { return sv(s.ClusterName) },
		listed: func(s smtypes.ClusterSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerCluster, s.ClusterArn, s.ClusterName, s.CreationTime, string(s.ClusterStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.ClusterSummary) (*store.Resource, error) {
			name := sv(s.ClusterName)
			out, err := client.DescribeCluster(ctx, &sagemaker.DescribeClusterInput{ClusterName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.ClusterArn)
			if arn == "" {
				return nil, nil
			}
			cname := sv(out.ClusterName)
			status := string(out.ClusterStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerCluster,
				NativeID:       arn,
				Name:           &cname,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListClusters"))
}

// scanSageMakerWorkteams lists ground-truth workteams then fans out
// DescribeWorkteam for the full body, matching every other SageMaker
// family's per-Describe rule.
func scanSageMakerWorkteams(ctx context.Context, client sagemakerMiscAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.Workteam]{
		listOp: "sagemaker:ListWorkteams", describeOp: "sagemaker:DescribeWorkteam",
		list: func(ctx context.Context, token *string) ([]smtypes.Workteam, *string, error) {
			out, err := client.ListWorkteams(ctx, &sagemaker.ListWorkteamsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Workteams, out.NextToken, nil
		},
		name: func(s smtypes.Workteam) string { return sv(s.WorkteamName) },
		listed: func(s smtypes.Workteam) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerWorkteam, s.WorkteamArn, s.WorkteamName, s.CreateDate, "", s)
		},
		describe: func(ctx context.Context, s smtypes.Workteam) (*store.Resource, error) {
			name := sv(s.WorkteamName)
			out, err := client.DescribeWorkteam(ctx, &sagemaker.DescribeWorkteamInput{WorkteamName: &name})
			if err != nil {
				return nil, err
			}
			if out.Workteam == nil {
				return nil, nil
			}
			arn := sv(out.Workteam.WorkteamArn)
			if arn == "" {
				return nil, nil
			}
			wname := sv(out.Workteam.WorkteamName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerWorkteam,
				NativeID:       arn,
				Name:           &wname,
				Region:         &region,
				CreatedAt:      tp(out.Workteam.CreateDate),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListWorkteams"))
}

// scanSageMakerClusterNodes lists the instances of every scanned HyperPod
// cluster. Only nodes with an assigned InstanceId are listed (the API default):
// a node still provisioning has no stable id yet.
func scanSageMakerClusterNodes(ctx context.Context, client sagemakerMiscAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSageMakerChildren(ctx, acct, region, st, scanID, TypeSageMakerCluster, "sagemaker:ListClusterNodes", []string{"ResourceNotFound"},
		sagemakerChildSkips{emptyWhy: "cluster nodes have an empty InstanceId"},
		func(ctx context.Context, cluster store.Resource) []sagemakerChildList[*store.Resource] {
			nodes, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]smtypes.ClusterNodeSummary, *string, error) {
				out, err := client.ListClusterNodes(ctx, &sagemaker.ListClusterNodesInput{
					ClusterName: &cluster.NativeID, MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				return out.ClusterNodeSummaries, out.NextToken, nil
			})
			l := sagemakerChildList[*store.Resource]{key: cluster.NativeID, err: err}
			for i, n := range nodes {
				id := sv(n.InstanceId)
				if id == "" {
					l.skipped = append(l.skipped, fmt.Sprintf("%s #%d", cluster.NativeID, i+1))
					continue
				}
				res := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeSageMakerClusterNode,
					NativeID:       cluster.NativeID + "/node/" + id,
					Name:           &id,
					Region:         &region,
					CreatedAt:      tp(n.LaunchTime),
					AttributesJSON: mustJSON(n),
					DiscoveredBy:   scanID,
				}
				if n.InstanceStatus != nil && n.InstanceStatus.Status != "" {
					status := string(n.InstanceStatus.Status)
					res.Status = &status
				}
				l.items = append(l.items, res)
			}
			return []sagemakerChildList[*store.Resource]{l}
		})
}

// scanSageMakerSubscribedWorkteams lists the vendor workteams this account
// subscribed to through AWS Marketplace. Their ARNs name the vendor's account,
// so they never collide with the private workteams ListWorkteams returns.
func scanSageMakerSubscribedWorkteams(ctx context.Context, client sagemakerMiscAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSagemakerSummaryList(ctx, acct, region, st, scanID,
		TypeSageMakerSubscribedWorkteam, "sagemaker:ListSubscribedWorkteams",
		func(ctx context.Context, token *string) ([]smtypes.SubscribedWorkteam, *string, error) {
			out, err := client.ListSubscribedWorkteams(ctx, &sagemaker.ListSubscribedWorkteamsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SubscribedWorkteams, out.NextToken, nil
		},
		func(s smtypes.SubscribedWorkteam) (string, string, string, *time.Time) {
			// SubscribedWorkteam carries no timestamp or status.
			return sv(s.WorkteamArn), sv(s.MarketplaceTitle), "", nil
		},
		// Their tags are not read: a subscribed workteam is the vendor's
		// resource, in the vendor's account, so its tags are not this
		// account's to read.
		sagemakerStoreUntagged(st, acct, region, "sagemaker:ListSubscribedWorkteams"))
}
