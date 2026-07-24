package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/drs"
)

// AWS Service Reference names every DRS resource type with a "Resource" suffix
// (SourceServerResource, RecoveryInstanceResource, …); DRS has no CloudFormation
// twin, so disco types mirror that spelling exactly — the algorithmic key
// matches with no alias.
func init() {
	registerType(restype.Descriptor{Type: TypeDRSSourceServerResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSRecoveryInstanceResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSSourceNetworkResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSReplicationConfigurationTemplateResource, Service: "drs"})
	registerType(restype.Descriptor{Type: TypeDRSLaunchConfigurationTemplateResource, Service: "drs"})
	registerService(serviceEntry{
		name: "aws:drs",
		fn:   scanDRS,
	})
}

type drsAPI interface {
	DescribeSourceServers(context.Context, *drs.DescribeSourceServersInput, ...func(*drs.Options)) (*drs.DescribeSourceServersOutput, error)
	DescribeRecoveryInstances(context.Context, *drs.DescribeRecoveryInstancesInput, ...func(*drs.Options)) (*drs.DescribeRecoveryInstancesOutput, error)
	DescribeSourceNetworks(context.Context, *drs.DescribeSourceNetworksInput, ...func(*drs.Options)) (*drs.DescribeSourceNetworksOutput, error)
	DescribeReplicationConfigurationTemplates(context.Context, *drs.DescribeReplicationConfigurationTemplatesInput, ...func(*drs.Options)) (*drs.DescribeReplicationConfigurationTemplatesOutput, error)
	DescribeLaunchConfigurationTemplates(context.Context, *drs.DescribeLaunchConfigurationTemplatesInput, ...func(*drs.Options)) (*drs.DescribeLaunchConfigurationTemplatesOutput, error)
}

// scanDRS discovers DRS source servers, recovery instances, source networks,
// and replication / launch configuration templates. Jobs (ephemeral
// recovery/drill run records) are not scanned.
func scanDRS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := drs.NewFromConfig(acct.cfg, func(o *drs.Options) { o.Region = region })
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDRSSourceServers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDRSRecoveryInstances(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDRSSourceNetworks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDRSReplicationTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDRSLaunchTemplates(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			if isAccountNotInitialized(perr) {
				return 0, 0, markServiceDisabled(perr)
			}
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanDRSSourceServers(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := drs.NewDescribeSourceServersPaginator(client, &drs.DescribeSourceServersInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "drs:DescribeSourceServers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("drs:DescribeSourceServers: %w", err)
		}
		for _, s := range out.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSSourceServerResource, NativeID: arn,
				Name: s.SourceServerID, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "drs source-servers")
}

func scanDRSRecoveryInstances(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := drs.NewDescribeRecoveryInstancesPaginator(client, &drs.DescribeRecoveryInstancesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "drs:DescribeRecoveryInstances", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("drs:DescribeRecoveryInstances: %w", err)
		}
		for _, ri := range out.Items {
			arn := sv(ri.Arn)
			if arn == "" {
				continue
			}
			status := string(ri.Ec2InstanceState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSRecoveryInstanceResource, NativeID: arn,
				Name: ri.RecoveryInstanceID, Region: &region, Status: &status,
				AttributesJSON: mustJSON(ri), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "drs recovery-instances")
}

func scanDRSSourceNetworks(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := drs.NewDescribeSourceNetworksPaginator(client, &drs.DescribeSourceNetworksInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "drs:DescribeSourceNetworks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("drs:DescribeSourceNetworks: %w", err)
		}
		for _, sn := range out.Items {
			arn := sv(sn.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSSourceNetworkResource, NativeID: arn,
				Name: sn.SourceNetworkID, Region: &region, AttributesJSON: mustJSON(sn), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "drs source-networks")
}

func scanDRSReplicationTemplates(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := drs.NewDescribeReplicationConfigurationTemplatesPaginator(client, &drs.DescribeReplicationConfigurationTemplatesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "drs:DescribeReplicationConfigurationTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("drs:DescribeReplicationConfigurationTemplates: %w", err)
		}
		for _, rt := range out.Items {
			arn := sv(rt.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSReplicationConfigurationTemplateResource, NativeID: arn,
				Name: rt.ReplicationConfigurationTemplateID, Region: &region,
				AttributesJSON: mustJSON(rt), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "drs replication-configuration-templates")
}

func scanDRSLaunchTemplates(ctx context.Context, client drsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := drs.NewDescribeLaunchConfigurationTemplatesPaginator(client, &drs.DescribeLaunchConfigurationTemplatesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "drs:DescribeLaunchConfigurationTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("drs:DescribeLaunchConfigurationTemplates: %w", err)
		}
		for _, lt := range out.Items {
			arn := sv(lt.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDRSLaunchConfigurationTemplateResource, NativeID: arn,
				Name: lt.LaunchConfigurationTemplateID, Region: &region,
				AttributesJSON: mustJSON(lt), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "drs launch-configuration-templates")
}
