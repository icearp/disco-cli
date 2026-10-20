package aws

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2SnapshotAttribute, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2ImageAttribute, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2LockedSnapshot, Service: "ec2"})
	// One per Verified Access instance, created with it and never deletable on
	// its own (only modified). Still not Managed: the customer sets its logging
	// options, and Managed rows are left out of default `disco check` input.
	registerType(restype.Descriptor{Type: TypeEC2VerifiedAccessInstanceLoggingConfiguration, Service: "ec2", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeEC2ApplicationStatusCheck, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2RouteServerAssociation, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2RouteServerPropagation, Service: "ec2"})
}

const (
	ec2SnapshotAttributeOp             = "ec2:DescribeSnapshotAttribute"
	ec2ImageAttributeOp                = "ec2:DescribeImageAttribute"
	ec2LockedSnapshotsOp               = "ec2:DescribeLockedSnapshots"
	ec2VALoggingConfigurationsOp       = "ec2:DescribeVerifiedAccessInstanceLoggingConfigurations"
	ec2ApplicationStatusChecksOp       = "ec2:DescribeApplicationStatusChecks"
	ec2RouteServerAssociationsOp       = "ec2:GetRouteServerAssociations"
	ec2RouteServerPropagationsOp       = "ec2:GetRouteServerPropagations"
	ec2LockedSnapshotsPageSize         = 1000 // DescribeLockedSnapshotsMaxResults maximum
	ec2ApplicationStatusChecksPageSize = 100  // DescribeApplicationStatusChecksMaxResults maximum
	ec2VALoggingConfigurationsPageSize = 10   // DescribeVerifiedAccessInstanceLoggingConfigurationsMaxResults maximum
	// ec2ImageDeregistered is the State of an AMI that can no longer be
	// launched, so who may launch it no longer matters.
	ec2ImageDeregistered = string(ec2types.ImageStateDeregistered)
	// ec2SnapshotError is the State of a snapshot whose creation failed, so no
	// volume can be created from it.
	ec2SnapshotError = string(ec2types.SnapshotStateError)
)

// The codes EC2 is expected to return for a parent that is gone, or not in a
// state to answer, by its child call. None was observed live, so a skip on
// any of them warns rather than passing silently.
var (
	ec2SnapshotSkipCodes = []skipCode{
		{"InvalidSnapshot.NotFound", "a snapshot deleted since it was listed"},
		{ec2IncorrectState, "a pending snapshot may refuse the call (unverified)"},
	}
	ec2ImageSkipCodes = []skipCode{
		{"InvalidAMIID.NotFound", "an AMI deregistered since it was listed"},
		{"InvalidAMIID.Unavailable", "a deregistered or failed AMI may refuse the call (unverified)"},
	}
	ec2RouteServerSkipCodes = []skipCode{
		{"InvalidRouteServerId.NotFound", "a route server deleted since it was listed"},
		{ec2IncorrectState, "a pending or deleting route server may refuse the call (unverified)"},
	}
)

// snapshotAttributeNativeID builds the NativeID of one attribute of a
// snapshot. AWS documents no ARN for it; the attribute name is the key
// DescribeSnapshotAttribute takes.
func snapshotAttributeNativeID(snapshotARN string, attr ec2types.SnapshotAttributeName) string {
	return snapshotARN + "/attribute/" + string(attr)
}

// imageAttributeNativeID is snapshotAttributeNativeID for an AMI.
func imageAttributeNativeID(imageARN string, attr ec2types.ImageAttributeName) string {
	return imageARN + "/attribute/" + string(attr)
}

// lockedSnapshotNativeID builds the NativeID of the lock on one snapshot. AWS
// documents no ARN for it; a snapshot has at most one lock.
func lockedSnapshotNativeID(region, accountID, snapshotID string) string {
	return ec2ARN(region, accountID, "snapshot", snapshotID) + "/lock"
}

// vaLoggingConfigurationNativeID builds the NativeID of the logging
// configuration of one Verified Access instance. AWS documents no ARN for it.
// That an instance has one and only one is inferred from the API shape
// (ModifyVerifiedAccessInstanceLoggingConfiguration, no create or delete),
// unverified live.
func vaLoggingConfigurationNativeID(region, accountID, instanceID string) string {
	return ec2ARN(region, accountID, "verified-access-instance", instanceID) + "/logging-configuration"
}

// routeServerAssociationNativeID builds the NativeID of the association of a
// route server with a VPC. AWS documents no ARN for it; the VPC id is the key
// DisassociateRouteServer takes beside the route server.
func routeServerAssociationNativeID(routeServerARN, vpcID string) string {
	return routeServerARN + "/association/" + vpcID
}

// routeServerPropagationNativeID builds the NativeID of the propagation of a
// route server into a route table. AWS documents no ARN for it; the route
// table id is the key DisableRouteServerPropagation takes beside the route
// server.
func routeServerPropagationNativeID(routeServerARN, routeTableID string) string {
	return routeServerARN + "/propagation/" + routeTableID
}

// snapshotVolumePermissions is the stored payload of a snapshot's
// createVolumePermission attribute: the output without its envelope and
// without the attributes the call did not ask for.
type snapshotVolumePermissions struct {
	SnapshotID              *string `json:"SnapshotId"`
	CreateVolumePermissions []ec2types.CreateVolumePermission
}

// imageLaunchPermissions is snapshotVolumePermissions for an AMI's
// launchPermission attribute.
type imageLaunchPermissions struct {
	ImageID           *string `json:"ImageId"`
	LaunchPermissions []ec2types.LaunchPermission
}

// sortVolumePermissions orders the list totally, so that the order EC2
// returns it in cannot change the stored row.
func sortVolumePermissions(perms []ec2types.CreateVolumePermission) {
	slices.SortFunc(perms, func(a, b ec2types.CreateVolumePermission) int {
		return cmp.Or(strings.Compare(string(a.Group), string(b.Group)), strings.Compare(sv(a.UserId), sv(b.UserId)))
	})
}

// sortLaunchPermissions is sortVolumePermissions for launch permissions;
// DescribeImageAttribute documents that its element order might vary.
func sortLaunchPermissions(perms []ec2types.LaunchPermission) {
	slices.SortFunc(perms, func(a, b ec2types.LaunchPermission) int {
		return cmp.Or(
			strings.Compare(string(a.Group), string(b.Group)),
			strings.Compare(sv(a.UserId), sv(b.UserId)),
			strings.Compare(sv(a.OrganizationArn), sv(b.OrganizationArn)),
			strings.Compare(sv(a.OrganizationalUnitArn), sv(b.OrganizationalUnitArn)),
		)
	})
}

// scanEC2SnapshotAttributes stores, under each snapshot this scan stored in
// region (all owned by this account) except failed ones, who may create
// volumes from it: one row per snapshot, an empty list included, so that
// sharing or unsharing it changes that row. Group "all" in the list means
// public. It costs one call per snapshot; the rows are written in one upsert.
// ec2StoredParents reads whole rows, attributes included, as the store has no
// lighter read, so every owned snapshot in the region is in memory at once;
// accepted because the fan-out over them costs one API call each anyway.
func scanEC2SnapshotAttributes(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	snapshots, err := ec2StoredParents(st, acct, region, scanID, TypeEC2Snapshot, "snapshot", ec2SnapshotError)
	if err != nil {
		return 0, 0, err
	}
	attr := ec2types.SnapshotAttributeNameCreateVolumePermission
	return childFanOutCollectSkipCodes(ctx, st, acct, region, ec2SnapshotAttributeOp, snapshots, neverSkip, byParentID, "snapshot(s)", ec2SnapshotSkipCodes,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			out, err := client.DescribeSnapshotAttribute(ctx, &ec2.DescribeSnapshotAttributeInput{SnapshotId: &p.id, Attribute: attr})
			if err != nil {
				return nil, err
			}
			sortVolumePermissions(out.CreateVolumePermissions)
			return []*store.Resource{{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2SnapshotAttribute,
				NativeID:       snapshotAttributeNativeID(p.arn, attr),
				Region:         &region,
				AttributesJSON: mustJSON(snapshotVolumePermissions{SnapshotID: out.SnapshotId, CreateVolumePermissions: out.CreateVolumePermissions}),
				DiscoveredBy:   scanID,
			}}, nil
		})
}

// scanEC2ImageAttributes stores, under each AMI this scan stored in region
// (all owned by this account) except deregistered ones, who may launch it:
// accounts, organizations, OUs, or group "all" for public; one row per AMI,
// an empty list included. Only launchPermission is read: every other
// attribute but uefiData is a field of the DescribeImages element the AMI row
// already stores, and uefiData is the AMI's UEFI variable store, boot data
// that says nothing of who may use the AMI. It costs one call per AMI; the
// rows are written in one upsert. Every owned AMI row in the region is read
// into memory at once, as for snapshot attributes.
func scanEC2ImageAttributes(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	images, err := ec2StoredParents(st, acct, region, scanID, TypeEC2Image, "image", ec2ImageDeregistered)
	if err != nil {
		return 0, 0, err
	}
	attr := ec2types.ImageAttributeNameLaunchPermission
	return childFanOutCollectSkipCodes(ctx, st, acct, region, ec2ImageAttributeOp, images, neverSkip, byParentID, "AMI(s)", ec2ImageSkipCodes,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			out, err := client.DescribeImageAttribute(ctx, &ec2.DescribeImageAttributeInput{ImageId: &p.id, Attribute: attr})
			if err != nil {
				return nil, err
			}
			sortLaunchPermissions(out.LaunchPermissions)
			return []*store.Resource{{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2ImageAttribute,
				NativeID:       imageAttributeNativeID(p.arn, attr),
				Region:         &region,
				AttributesJSON: mustJSON(imageLaunchPermissions{ImageID: out.ImageId, LaunchPermissions: out.LaunchPermissions}),
				DiscoveredBy:   scanID,
			}}, nil
		})
}

// scanEC2LockedSnapshots stores the lock of each snapshot this account owns,
// linked under the snapshot when this scan stored it in region. A lock that
// names another account as owner is dropped: its NativeID would carry this
// account's id. One that names no owner is kept: a missing field is no sign
// of another owner, and dropping it would lose the lock silently. An expired
// lock is kept until AWS stops listing it. The stored snapshots are read only
// when a lock came back. DescribeLockedSnapshots has no SDK paginator.
func scanEC2LockedSnapshots(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		locks   []ec2types.LockedSnapshotsInfo
		token   *string
		listErr error
	)
	for {
		page, err := client.DescribeLockedSnapshots(ctx, &ec2.DescribeLockedSnapshotsInput{
			MaxResults: sdkaws.Int32(ec2LockedSnapshotsPageSize),
			NextToken:  token,
		})
		if err != nil {
			if token == nil && isEC2RegionFeatureGap(err) {
				return 0, 0, nil
			}
			listErr = err
			break
		}
		for _, s := range page.Snapshots {
			if owner := sv(s.OwnerId); sv(s.SnapshotId) == "" || (owner != "" && owner != acct.ID) {
				continue
			}
			locks = append(locks, s)
		}
		next := sv(page.NextToken)
		if next == "" || next == sv(token) {
			break
		}
		token = page.NextToken
	}
	var stored map[string]string
	if len(locks) > 0 {
		var err error
		if stored, err = storedParentIDs(st, acct, region, scanID, TypeEC2Snapshot, "snapshot"); err != nil {
			return 0, 0, errors.Join(listErr, err)
		}
	}
	children := make([]containedChild, len(locks))
	for i, s := range locks {
		id := sv(s.SnapshotId)
		children[i] = containedChild{
			row: &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2LockedSnapshot,
				NativeID:       lockedSnapshotNativeID(region, acct.ID, id),
				Region:         &region,
				Status:         nonEmptyPtr(string(s.LockState)),
				CreatedAt:      tp(s.LockCreatedOn),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			},
			parentID: stored[id],
		}
	}
	return upsertRegionChildren(st, acct, region, ec2LockedSnapshotsOp, children, listErr)
}

// scanEC2VerifiedAccessLoggingConfigurations stores the logging configuration
// of each Verified Access instance the API lists, linked under the instance
// when this scan stored it in region. Whether the list carries a row for an
// instance whose logging was never configured is unverified.
func scanEC2VerifiedAccessLoggingConfigurations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	stored, err := storedParentIDs(st, acct, region, scanID, TypeEC2VerifiedAccessInstance, "verified-access-instance")
	if err != nil {
		return 0, 0, err
	}
	pager := ec2.NewDescribeVerifiedAccessInstanceLoggingConfigurationsPaginator(client,
		&ec2.DescribeVerifiedAccessInstanceLoggingConfigurationsInput{},
		func(o *ec2.DescribeVerifiedAccessInstanceLoggingConfigurationsPaginatorOptions) {
			o.Limit = ec2VALoggingConfigurationsPageSize
			o.StopOnDuplicateToken = true
		})
	var children []containedChild
	for first := true; pager.HasMorePages(); first = false {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if first && isEC2RegionFeatureGap(err) {
				return 0, 0, nil
			}
			return upsertRegionChildren(st, acct, region, ec2VALoggingConfigurationsOp, children, err)
		}
		for _, c := range page.LoggingConfigurations {
			id := sv(c.VerifiedAccessInstanceId)
			if id == "" {
				continue
			}
			children = append(children, containedChild{
				row: &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VerifiedAccessInstanceLoggingConfiguration,
					NativeID:       vaLoggingConfigurationNativeID(region, acct.ID, id),
					Region:         &region,
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				},
				parentID: stored[id],
			})
		}
	}
	return upsertRegionChildren(st, acct, region, ec2VALoggingConfigurationsOp, children, nil)
}

// scanEC2ApplicationStatusChecks stores the application status checks: the
// health check configurations, not their results (DescribeApplicationStatus
// reports those). Checks in their deletion grace period are left out by the
// API unless IncludeAll is set. DescribeApplicationStatusChecks has no SDK
// paginator.
func scanEC2ApplicationStatusChecks(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var children []containedChild
	var token *string
	for {
		page, err := client.DescribeApplicationStatusChecks(ctx, &ec2.DescribeApplicationStatusChecksInput{
			MaxResults: sdkaws.Int32(ec2ApplicationStatusChecksPageSize),
			NextToken:  token,
		})
		if err != nil {
			if token == nil && isEC2RegionFeatureGap(err) {
				return 0, 0, nil
			}
			return upsertRegionChildren(st, acct, region, ec2ApplicationStatusChecksOp, children, err)
		}
		for _, c := range page.ApplicationStatusChecks {
			id := sv(c.ApplicationStatusCheckId)
			if id == "" {
				continue
			}
			children = append(children, containedChild{row: &store.Resource{
				Provider:    "aws",
				AccountID:   acct.ID,
				AccountName: &acct.Name,
				Type:        TypeEC2ApplicationStatusCheck,
				// Kind from the Service Reference ARN format
				// application-status-check/${ApplicationStatusCheckId}.
				NativeID:       ec2ARN(region, acct.ID, "application-status-check", id),
				Name:           ec2TagName(c.Tags),
				Region:         &region,
				CreatedAt:      tp(c.CreationTime),
				TagsJSON:       awsTagsJSON(c.Tags),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			}})
		}
		next := sv(page.NextToken)
		if next == "" || next == sv(token) {
			break
		}
		token = page.NextToken
	}
	return upsertRegionChildren(st, acct, region, ec2ApplicationStatusChecksOp, children, nil)
}

// scanEC2RouteServerAssociations stores, under each route server this scan
// stored in region except deleted ones, its VPC associations. The model has
// no disassociated state, so none is dropped.
func scanEC2RouteServerAssociations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	servers, err := ec2StoredParents(st, acct, region, scanID, TypeEC2RouteServer, "route-server", string(ec2types.RouteServerStateDeleted))
	if err != nil {
		return 0, 0, err
	}
	return childFanOutCollectSkipCodes(ctx, st, acct, region, ec2RouteServerAssociationsOp, servers, neverSkip, byParentID, "route server(s)", ec2RouteServerSkipCodes,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			out, err := client.GetRouteServerAssociations(ctx, &ec2.GetRouteServerAssociationsInput{RouteServerId: &p.id})
			if err != nil {
				return nil, err
			}
			var rows []*store.Resource
			for _, a := range out.RouteServerAssociations {
				vpcID := sv(a.VpcId)
				if vpcID == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2RouteServerAssociation,
					NativeID:       routeServerAssociationNativeID(p.arn, vpcID),
					Name:           a.VpcId,
					Region:         &region,
					Status:         nonEmptyPtr(string(a.State)),
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			return rows, nil
		})
}

// scanEC2RouteServerPropagations stores, under each route server this scan
// stored in region except deleted ones, its route table propagations. The
// model has no deleted state for a propagation, so none is dropped.
func scanEC2RouteServerPropagations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	servers, err := ec2StoredParents(st, acct, region, scanID, TypeEC2RouteServer, "route-server", string(ec2types.RouteServerStateDeleted))
	if err != nil {
		return 0, 0, err
	}
	return childFanOutCollectSkipCodes(ctx, st, acct, region, ec2RouteServerPropagationsOp, servers, neverSkip, byParentID, "route server(s)", ec2RouteServerSkipCodes,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			out, err := client.GetRouteServerPropagations(ctx, &ec2.GetRouteServerPropagationsInput{RouteServerId: &p.id})
			if err != nil {
				return nil, err
			}
			var rows []*store.Resource
			for _, pr := range out.RouteServerPropagations {
				routeTableID := sv(pr.RouteTableId)
				if routeTableID == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2RouteServerPropagation,
					NativeID:       routeServerPropagationNativeID(p.arn, routeTableID),
					Name:           pr.RouteTableId,
					Region:         &region,
					Status:         nonEmptyPtr(string(pr.State)),
					AttributesJSON: mustJSON(pr),
					DiscoveredBy:   scanID,
				})
			}
			return rows, nil
		})
}

// storedParentIDs maps the id of each parentType row this scan stored in
// region to its ResourceID, for a region-wide child listing that names its
// parent by id: only a stored parent can contain the child.
func storedParentIDs(st *store.Store, acct *account, region, scanID, parentType, kind string) (map[string]string, error) {
	parents, err := ec2StoredParents(st, acct, region, scanID, parentType, kind)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(parents))
	for _, p := range parents {
		ids[p.id] = store.ResourceID("aws", acct.ID, p.arn)
	}
	return ids, nil
}
