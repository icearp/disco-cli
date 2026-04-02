package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"golang.org/x/sync/errgroup"
)

// scanEC2 discovers instances, VPCs, subnets, security groups, EBS volumes,
// and internet gateways in one region, running all sub-scanners in parallel.
func scanEC2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := ec2.NewFromConfig(acct.cfg, func(o *ec2.Options) { o.Region = region })

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return scanInstances(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSubnets(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSecurityGroups(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVolumes(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanInternetGateways(ctx, client, acct, region, st, scanID) })
	return g.Wait()
}

func scanInstances(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	pager := ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("ec2:DescribeInstances", acct.ID, region, err)
			}
			return fmt.Errorf("ec2:DescribeInstances: %w", err)
		}
		var batch []*store.Resource
		for _, res := range page.Reservations {
			for _, inst := range res.Instances {
				status := string(inst.State.Name)
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           "aws:ec2:instance",
					NativeID:       ec2ARN(region, acct.ID, "instance", sv(inst.InstanceId)),
					Name:           ec2TagName(inst.Tags),
					Region:         &region,
					Status:         &status,
					TagsJSON:       ec2TagsJSON(inst.Tags),
					AttributesJSON: mustJSON(inst),
					ScanID:         scanID,
				}
				batch = append(batch, r)
			}
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert instances: %w", err)
			}
		}
	}
	return nil
}

func scanVPCs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	pager := ec2.NewDescribeVpcsPaginator(client, &ec2.DescribeVpcsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("ec2:DescribeVpcs", acct.ID, region, err)
			}
			return fmt.Errorf("ec2:DescribeVpcs: %w", err)
		}
		var batch []*store.Resource
		for _, vpc := range page.Vpcs {
			status := string(vpc.State)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:ec2:vpc",
				NativeID:       ec2ARN(region, acct.ID, "vpc", sv(vpc.VpcId)),
				Name:           ec2TagName(vpc.Tags),
				Region:         &region,
				Status:         &status,
				TagsJSON:       ec2TagsJSON(vpc.Tags),
				AttributesJSON: mustJSON(vpc),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert VPCs: %w", err)
			}
		}
	}
	return nil
}

func scanSubnets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	pager := ec2.NewDescribeSubnetsPaginator(client, &ec2.DescribeSubnetsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("ec2:DescribeSubnets", acct.ID, region, err)
			}
			return fmt.Errorf("ec2:DescribeSubnets: %w", err)
		}
		var batch []*store.Resource
		for _, sn := range page.Subnets {
			status := string(sn.State)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:ec2:subnet",
				NativeID:       ec2ARN(region, acct.ID, "subnet", sv(sn.SubnetId)),
				Name:           ec2TagName(sn.Tags),
				Region:         &region,
				Status:         &status,
				TagsJSON:       ec2TagsJSON(sn.Tags),
				AttributesJSON: mustJSON(sn),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert subnets: %w", err)
			}
		}
	}
	return nil
}

func scanSecurityGroups(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	pager := ec2.NewDescribeSecurityGroupsPaginator(client, &ec2.DescribeSecurityGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("ec2:DescribeSecurityGroups", acct.ID, region, err)
			}
			return fmt.Errorf("ec2:DescribeSecurityGroups: %w", err)
		}
		var batch []*store.Resource
		for _, sg := range page.SecurityGroups {
			name := sv(sg.GroupName)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:ec2:security-group",
				NativeID:       ec2ARN(region, acct.ID, "security-group", sv(sg.GroupId)),
				Name:           &name,
				Region:         &region,
				TagsJSON:       ec2TagsJSON(sg.Tags),
				AttributesJSON: mustJSON(sg),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert security groups: %w", err)
			}
		}
	}
	return nil
}

func scanVolumes(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	pager := ec2.NewDescribeVolumesPaginator(client, &ec2.DescribeVolumesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("ec2:DescribeVolumes", acct.ID, region, err)
			}
			return fmt.Errorf("ec2:DescribeVolumes: %w", err)
		}
		var batch []*store.Resource
		for _, vol := range page.Volumes {
			status := string(vol.State)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:ec2:volume",
				NativeID:       ec2ARN(region, acct.ID, "volume", sv(vol.VolumeId)),
				Name:           ec2TagName(vol.Tags),
				Region:         &region,
				Status:         &status,
				TagsJSON:       ec2TagsJSON(vol.Tags),
				AttributesJSON: mustJSON(vol),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert volumes: %w", err)
			}
		}
	}
	return nil
}

func scanInternetGateways(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	pager := ec2.NewDescribeInternetGatewaysPaginator(client, &ec2.DescribeInternetGatewaysInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("ec2:DescribeInternetGateways", acct.ID, region, err)
			}
			return fmt.Errorf("ec2:DescribeInternetGateways: %w", err)
		}
		var batch []*store.Resource
		for _, igw := range page.InternetGateways {
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:ec2:internet-gateway",
				NativeID:       ec2ARN(region, acct.ID, "internet-gateway", sv(igw.InternetGatewayId)),
				Name:           ec2TagName(igw.Tags),
				Region:         &region,
				TagsJSON:       ec2TagsJSON(igw.Tags),
				AttributesJSON: mustJSON(igw),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert internet gateways: %w", err)
			}
		}
	}
	return nil
}

// ec2TagName extracts the "Name" tag value, returning nil if absent.
func ec2TagName(tags []ec2types.Tag) *string {
	for _, t := range tags {
		if sv(t.Key) == "Name" && t.Value != nil {
			return t.Value
		}
	}
	return nil
}

// ec2TagsJSON converts EC2 tag slices to a JSON-encoded map string pointer.
func ec2TagsJSON(tags []ec2types.Tag) *string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key != nil && t.Value != nil {
			m[*t.Key] = *t.Value
		}
	}
	s := mustJSON(m)
	return &s
}
