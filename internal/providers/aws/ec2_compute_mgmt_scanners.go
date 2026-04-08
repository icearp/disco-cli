package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"
)

// scanEC2ComputeMgmt discovers core compute types and compute management types:
// instances, security groups, volumes, launch templates, key pairs, placement
// groups, spot fleets, dedicated hosts, capacity reservations, and instance
// connect endpoints.
func scanEC2ComputeMgmt(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return scanInstances(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSecurityGroups(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVolumes(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanLaunchTemplates(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanKeyPairs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanPlacementGroups(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSpotFleets(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanHosts(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanCapacityReservations(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanInstanceConnectEndpoints(ctx, client, acct, region, st, scanID) })
	return g.Wait()
}

func scanInstances(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeInstances", acct, region, st,
		ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{}),
		func(page *ec2.DescribeInstancesOutput) []*store.Resource {
			var out []*store.Resource
			for _, res := range page.Reservations {
				for _, inst := range res.Instances {
					status := string(inst.State.Name)
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEC2Instance,
						NativeID:       ec2ARN(region, acct.ID, "instance", sv(inst.InstanceId)),
						Name:           ec2TagName(inst.Tags),
						Region:         &region,
						Zone:           inst.Placement.AvailabilityZoneId,
						CreatedAt:      tp(inst.LaunchTime),
						Status:         &status,
						TagsJSON:       awsTagsJSON(inst.Tags),
						AttributesJSON: mustJSON(inst),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
}

func scanSecurityGroups(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeSecurityGroups", acct, region, st,
		ec2.NewDescribeSecurityGroupsPaginator(client, &ec2.DescribeSecurityGroupsInput{}),
		func(page *ec2.DescribeSecurityGroupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sg := range page.SecurityGroups {
				name := sv(sg.GroupName)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SecurityGroup,
					NativeID:       ec2ARN(region, acct.ID, "security-group", sv(sg.GroupId)),
					Name:           &name,
					Region:         &region,
					TagsJSON:       awsTagsJSON(sg.Tags),
					AttributesJSON: mustJSON(sg),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanVolumes(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeVolumes", acct, region, st,
		ec2.NewDescribeVolumesPaginator(client, &ec2.DescribeVolumesInput{}),
		func(page *ec2.DescribeVolumesOutput) []*store.Resource {
			var out []*store.Resource
			for _, vol := range page.Volumes {
				status := string(vol.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2Volume,
					NativeID:       ec2ARN(region, acct.ID, "volume", sv(vol.VolumeId)),
					Name:           ec2TagName(vol.Tags),
					Region:         &region,
					Zone:           vol.AvailabilityZoneId,
					Status:         &status,
					TagsJSON:       awsTagsJSON(vol.Tags),
					AttributesJSON: mustJSON(vol),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanLaunchTemplates(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeLaunchTemplates", acct, region, st,
		ec2.NewDescribeLaunchTemplatesPaginator(client, &ec2.DescribeLaunchTemplatesInput{}),
		func(page *ec2.DescribeLaunchTemplatesOutput) []*store.Resource {
			var out []*store.Resource
			for _, lt := range page.LaunchTemplates {
				name := sv(lt.LaunchTemplateName)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2LaunchTemplate,
					NativeID:       ec2ARN(region, acct.ID, "launch-template", sv(lt.LaunchTemplateId)),
					Name:           &name,
					Region:         &region,
					CreatedAt:      tp(lt.CreateTime),
					TagsJSON:       awsTagsJSON(lt.Tags),
					AttributesJSON: mustJSON(lt),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanKeyPairs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribeKeyPairs has no paginator; all results returned in one call.
	out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeKeyPairs", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeKeyPairs: %w", err)
	}
	var batch []*store.Resource
	for _, kp := range out.KeyPairs {
		name := sv(kp.KeyName)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2KeyPair,
			NativeID:       ec2ARN(region, acct.ID, "key-pair", sv(kp.KeyPairId)),
			Name:           &name,
			Region:         &region,
			CreatedAt:      tp(kp.CreateTime),
			TagsJSON:       awsTagsJSON(kp.Tags),
			AttributesJSON: mustJSON(kp),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert key pairs: %w", err)
		}
	}
	return nil
}

func scanPlacementGroups(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribePlacementGroups has no paginator; all results returned in one call.
	out, err := client.DescribePlacementGroups(ctx, &ec2.DescribePlacementGroupsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribePlacementGroups", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribePlacementGroups: %w", err)
	}
	var batch []*store.Resource
	for _, pg := range out.PlacementGroups {
		status := string(pg.State)
		name := sv(pg.GroupName)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2PlacementGroup,
			NativeID:       ec2ARN(region, acct.ID, "placement-group", sv(pg.GroupId)),
			Name:           &name,
			Region:         &region,
			Status:         &status,
			TagsJSON:       awsTagsJSON(pg.Tags),
			AttributesJSON: mustJSON(pg),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert placement groups: %w", err)
		}
	}
	return nil
}

func scanSpotFleets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeSpotFleetRequests", acct, region, st,
		ec2.NewDescribeSpotFleetRequestsPaginator(client, &ec2.DescribeSpotFleetRequestsInput{}),
		func(page *ec2.DescribeSpotFleetRequestsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sf := range page.SpotFleetRequestConfigs {
				status := string(sf.SpotFleetRequestState)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SpotFleet,
					NativeID:       ec2ARN(region, acct.ID, "spot-fleet-request", sv(sf.SpotFleetRequestId)),
					Region:         &region,
					CreatedAt:      tp(sf.CreateTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(sf.Tags),
					AttributesJSON: mustJSON(sf),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanHosts(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeHosts", acct, region, st,
		ec2.NewDescribeHostsPaginator(client, &ec2.DescribeHostsInput{}),
		func(page *ec2.DescribeHostsOutput) []*store.Resource {
			var out []*store.Resource
			for _, h := range page.Hosts {
				status := string(h.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2Host,
					NativeID:       ec2ARN(region, acct.ID, "dedicated-host", sv(h.HostId)),
					Name:           ec2TagName(h.Tags),
					Region:         &region,
					Zone:           h.AvailabilityZoneId,
					Status:         &status,
					TagsJSON:       awsTagsJSON(h.Tags),
					AttributesJSON: mustJSON(h),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanCapacityReservations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeCapacityReservations", acct, region, st,
		ec2.NewDescribeCapacityReservationsPaginator(client, &ec2.DescribeCapacityReservationsInput{}),
		func(page *ec2.DescribeCapacityReservationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, cr := range page.CapacityReservations {
				status := string(cr.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2CapacityReservation,
					NativeID:       sv(cr.CapacityReservationArn),
					Name:           ec2TagName(cr.Tags),
					Region:         &region,
					Zone:           cr.AvailabilityZoneId,
					CreatedAt:      tp(cr.CreateDate),
					Status:         &status,
					TagsJSON:       awsTagsJSON(cr.Tags),
					AttributesJSON: mustJSON(cr),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanInstanceConnectEndpoints(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeInstanceConnectEndpoints", acct, region, st,
		ec2.NewDescribeInstanceConnectEndpointsPaginator(client, &ec2.DescribeInstanceConnectEndpointsInput{}),
		func(page *ec2.DescribeInstanceConnectEndpointsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ice := range page.InstanceConnectEndpoints {
				status := string(ice.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2InstanceConnectEndpoint,
					NativeID:       sv(ice.InstanceConnectEndpointArn),
					Name:           ec2TagName(ice.Tags),
					Region:         &region,
					CreatedAt:      tp(ice.CreatedAt),
					Status:         &status,
					TagsJSON:       awsTagsJSON(ice.Tags),
					AttributesJSON: mustJSON(ice),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
