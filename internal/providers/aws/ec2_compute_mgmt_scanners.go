package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"
)

// scanEC2ComputeMgmt discovers all compute resources: instances, security groups,
// volumes, launch templates, key pairs, placement groups, spot fleets, dedicated
// hosts, capacity reservations, instance connect endpoints, capacity reservation
// fleets, EC2 fleets, security group VPC associations, and snapshot block public
// access settings.
func scanEC2ComputeMgmt(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { tt, nn, e := scanInstances(ctx, client, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error {
		tt, nn, e := scanSecurityGroups(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error { tt, nn, e := scanVolumes(ctx, client, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error {
		tt, nn, e := scanLaunchTemplates(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error { tt, nn, e := scanKeyPairs(ctx, client, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error {
		tt, nn, e := scanPlacementGroups(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanSpotFleets(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error { tt, nn, e := scanHosts(ctx, client, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error {
		tt, nn, e := scanCapacityReservations(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanInstanceConnectEndpoints(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCapacityReservationFleets(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error { tt, nn, e := scanEC2Fleets(ctx, client, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error {
		tt, nn, e := scanSecurityGroupVPCAssociations(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanSnapshotBlockPublicAccess(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

func scanInstances(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanSecurityGroups(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanVolumes(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanLaunchTemplates(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanKeyPairs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// DescribeKeyPairs has no paginator; all results returned in one call.
	out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("ec2:DescribeKeyPairs", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ec2:DescribeKeyPairs: %w", err)
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
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert key pairs: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

func scanPlacementGroups(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// DescribePlacementGroups has no paginator; all results returned in one call.
	out, err := client.DescribePlacementGroups(ctx, &ec2.DescribePlacementGroupsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("ec2:DescribePlacementGroups", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ec2:DescribePlacementGroups: %w", err)
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
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert placement groups: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

func scanSpotFleets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanHosts(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanCapacityReservations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanInstanceConnectEndpoints(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanCapacityReservationFleets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeCapacityReservationFleets", acct, region, st,
		ec2.NewDescribeCapacityReservationFleetsPaginator(client, &ec2.DescribeCapacityReservationFleetsInput{}),
		func(page *ec2.DescribeCapacityReservationFleetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, fleet := range page.CapacityReservationFleets {
				status := string(fleet.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2CapacityReservationFleet,
					NativeID:       sv(fleet.CapacityReservationFleetArn),
					Region:         &region,
					CreatedAt:      tp(fleet.CreateTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(fleet.Tags),
					AttributesJSON: mustJSON(fleet),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanEC2Fleets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeFleets", acct, region, st,
		ec2.NewDescribeFleetsPaginator(client, &ec2.DescribeFleetsInput{}),
		func(page *ec2.DescribeFleetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, fleet := range page.Fleets {
				status := string(fleet.FleetState)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2Fleet,
					NativeID:       ec2ARN(region, acct.ID, "fleet", sv(fleet.FleetId)),
					Region:         &region,
					CreatedAt:      tp(fleet.CreateTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(fleet.Tags),
					AttributesJSON: mustJSON(fleet),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanSecurityGroupVPCAssociations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeSecurityGroupVpcAssociations", acct, region, st,
		ec2.NewDescribeSecurityGroupVpcAssociationsPaginator(client, &ec2.DescribeSecurityGroupVpcAssociationsInput{}),
		func(page *ec2.DescribeSecurityGroupVpcAssociationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, assoc := range page.SecurityGroupVpcAssociations {
				status := string(assoc.State)
				nativeID := ec2ARN(region, acct.ID, "security-group-vpc-assoc",
					sv(assoc.GroupId)+"/"+sv(assoc.VpcId))
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SecurityGroupVPCAssociation,
					NativeID:       nativeID,
					Region:         &region,
					Status:         &status,
					AttributesJSON: mustJSON(assoc),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanSnapshotBlockPublicAccess retrieves the account-level snapshot block public access
// setting. There is one per account; NativeID omits region for stability.
func scanSnapshotBlockPublicAccess(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetSnapshotBlockPublicAccessState(ctx, &ec2.GetSnapshotBlockPublicAccessStateInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("ec2:GetSnapshotBlockPublicAccessState", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ec2:GetSnapshotBlockPublicAccessState: %w", err)
	}
	state := string(out.State)
	nativeID := ec2ARN("", acct.ID, "snapshot-block-public-access", acct.ID)
	n, err := st.UpsertResource(&store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeEC2SnapshotBlockPublicAccess,
		NativeID:       nativeID,
		Region:         &region,
		Status:         &state,
		AttributesJSON: mustJSON(map[string]string{"state": state}),
		DiscoveredBy:   scanID,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert snapshot-block-public-access: %w", err)
	}
	return 1, n, nil
}
