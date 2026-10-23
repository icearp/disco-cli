package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveMPV1OriginEndpointToChannel,
		EdgeDecl{TypeMediaPackageOriginEndpoint, TypeMediaPackageChannel, store.RelAttachedTo},
	)
	registerResolver(
		resolveMPV1AssetRefs,
		EdgeDecl{TypeMediaPackageAsset, TypeMediaPackagePackagingGroup, store.RelAttachedTo},
		EdgeDecl{TypeMediaPackageAsset, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeMediaPackageAsset, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		resolveMPV1PackagingConfigToGroup,
		EdgeDecl{TypeMediaPackagePackagingConfiguration, TypeMediaPackagePackagingGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveMPV2ChildrenToChannelGroup,
		EdgeDecl{TypeMediaPackageV2Channel, TypeMediaPackageV2ChannelGroup, store.RelAttachedTo},
		EdgeDecl{TypeMediaPackageV2OriginEndpoint, TypeMediaPackageV2Channel, store.RelAttachedTo},
		EdgeDecl{TypeMediaPackageV2ChannelPolicy, TypeMediaPackageV2Channel, store.RelAttachedTo},
		EdgeDecl{TypeMediaPackageV2OriginEndpointPolicy, TypeMediaPackageV2OriginEndpoint, store.RelAttachedTo},
	)
}

// mpSiblingARN is the ARN of the resource kind/id that shares arn's
// partition, service, region and account, and false when arn has fewer than
// six colon-separated parts. The service is taken from arn rather than
// written in, because the VOD resources live under mediapackage-vod, not
// mediapackage.
func mpSiblingARN(arn, kind, id string) (string, bool) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return "", false
	}
	return strings.Join(parts[:5], ":") + ":" + kind + "/" + id, true
}

// mpLinkSibling upserts an attached-to edge from r to the kind/id resource
// beside it (mpSiblingARN) when that target was scanned. An empty id links
// nothing and checks nothing, so a row with no reference is not reported
// for a NativeID it never needed to parse. A NativeID that is not an ARN comes back as decodeErr, costing r
// its edge; a failed write comes back as writeErr.
func mpLinkSibling(st *store.Store, acct *account, r store.Resource, kind, id string, scanned map[string]bool) (decodeErr, writeErr error) {
	if id == "" {
		return nil, nil
	}
	arn, ok := mpSiblingARN(r.NativeID, kind, id)
	if !ok {
		return fmt.Errorf("%s %s: NativeID is not an ARN", r.Type, r.NativeID), nil
	}
	tgtID := store.ResourceID("aws", acct.ID, arn)
	if !scanned[tgtID] {
		return nil, nil
	}
	if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
		return nil, fmt.Errorf("upsert %s %s → %s: %w", r.Type, r.NativeID, arn, err)
	}
	return nil, nil
}

// resolveMPV1OriginEndpointToChannel wires each origin-endpoint to its channel
// via ChannelId. A row that fails to decode is reported at the end so that it
// costs the other rows none of their edges.
func resolveMPV1OriginEndpointToChannel(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaPackageOriginEndpoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	chSet, err := scannedIDSet(acct, st, TypeMediaPackageChannel)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			ChannelID *string `json:"ChannelId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		derr, werr := mpLinkSibling(st, acct, r, "channels", sv(attrs.ChannelID), chSet)
		if werr != nil {
			return errors.Join(append(decodeErrs, werr)...)
		}
		if derr != nil {
			decodeErrs = append(decodeErrs, derr)
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveMPV1AssetRefs wires each VOD asset to its packaging group, source S3
// bucket, and the IAM role used to ingest from S3. A row that fails to decode
// is reported at the end so that it costs the other rows none of their edges.
func resolveMPV1AssetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaPackageAsset}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pgSet, err := scannedIDSet(acct, st, TypeMediaPackagePackagingGroup)
	if err != nil {
		return err
	}
	bktSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			PackagingGroupID *string `json:"PackagingGroupId"`
			SourceArn        *string `json:"SourceArn"`
			SourceRoleArn    *string `json:"SourceRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		derr, werr := mpLinkSibling(st, acct, r, "packaging-groups", sv(attrs.PackagingGroupID), pgSet)
		if werr != nil {
			return errors.Join(append(decodeErrs, werr)...)
		}
		if derr != nil {
			decodeErrs = append(decodeErrs, derr)
		}
		// SourceArn names an S3 object; the edge goes to its bucket.
		for _, ref := range []struct {
			arn     string
			scanned map[string]bool
		}{{s3BucketARNFromLocation(sv(attrs.SourceArn)), bktSet}, {sv(attrs.SourceRoleArn), roleSet}} {
			tgtID := store.ResourceID("aws", acct.ID, ref.arn)
			if ref.arn == "" || !ref.scanned[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return errors.Join(append(decodeErrs, fmt.Errorf("upsert %s %s → %s: %w", r.Type, r.NativeID, ref.arn, err))...)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveMPV1PackagingConfigToGroup wires each packaging-configuration to its
// packaging group via PackagingGroupId. A row that fails to decode is
// reported at the end so that it costs the other rows none of their edges.
func resolveMPV1PackagingConfigToGroup(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaPackagePackagingConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pgSet, err := scannedIDSet(acct, st, TypeMediaPackagePackagingGroup)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			PackagingGroupID *string `json:"PackagingGroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		derr, werr := mpLinkSibling(st, acct, r, "packaging-groups", sv(attrs.PackagingGroupID), pgSet)
		if werr != nil {
			return errors.Join(append(decodeErrs, werr)...)
		}
		if derr != nil {
			decodeErrs = append(decodeErrs, derr)
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveMPV2ChildrenToChannelGroup wires v2 hierarchy:
//
//	channel → channel-group (NativeID `…:channelGroup/{gn}/channel/{cn}`)
//	origin-endpoint → channel (NativeID `…/channel/{cn}/originEndpoint/{en}`)
//	channel-policy → channel (NativeID `…/channel/{cn}/policy`)
//	origin-endpoint-policy → origin-endpoint (NativeID `…/originEndpoint/{en}/policy`)
//
// All tails strip cleanly to the parent ARN.
func resolveMPV2ChildrenToChannelGroup(acct *account, st *store.Store) error {
	cgSet, err := scannedIDSet(acct, st, TypeMediaPackageV2ChannelGroup)
	if err != nil {
		return err
	}
	chSet, err := scannedIDSet(acct, st, TypeMediaPackageV2Channel)
	if err != nil {
		return err
	}
	epSet, err := scannedIDSet(acct, st, TypeMediaPackageV2OriginEndpoint)
	if err != nil {
		return err
	}
	emit := func(srcID, tgtType, tgtARN string, set map[string]bool) error {
		tgtID := store.ResourceID("aws", acct.ID, tgtARN)
		if !set[tgtID] {
			return nil
		}
		if err := st.UpsertRelationship(srcID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert mpv2 →%s: %w", tgtType, err)
		}
		return nil
	}
	// channel → channel-group: strip `/channel/{cn}` tail.
	chRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaPackageV2Channel}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range chRows {
		if i := strings.Index(r.NativeID, "/channel/"); i >= 0 {
			if err := emit(r.ID, TypeMediaPackageV2ChannelGroup, r.NativeID[:i], cgSet); err != nil {
				return err
			}
		}
	}
	// origin-endpoint → channel: strip `/originEndpoint/{en}` tail.
	epRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaPackageV2OriginEndpoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range epRows {
		if i := strings.Index(r.NativeID, "/originEndpoint/"); i >= 0 {
			if err := emit(r.ID, TypeMediaPackageV2Channel, r.NativeID[:i], chSet); err != nil {
				return err
			}
		}
	}
	// channel-policy → channel: strip trailing `/policy`.
	cpRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaPackageV2ChannelPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range cpRows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		if err := emit(r.ID, TypeMediaPackageV2Channel, parent, chSet); err != nil {
			return err
		}
	}
	// origin-endpoint-policy → origin-endpoint: strip trailing `/policy`.
	oepRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaPackageV2OriginEndpointPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range oepRows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		if err := emit(r.ID, TypeMediaPackageV2OriginEndpoint, parent, epSet); err != nil {
			return err
		}
	}
	return nil
}
