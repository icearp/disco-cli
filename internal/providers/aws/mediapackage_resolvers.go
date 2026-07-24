package aws

import (
	"encoding/json"
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

func mediapackageARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:mediapackage:%s:%s:%s/%s", region, acct, kind, id)
}

// resolveMPV1OriginEndpointToChannel wires each origin-endpoint to its channel
// via ChannelID (rebuild ARN as `arn:aws:mediapackage:r:a:channels/{id}`).
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
	for _, r := range rows {
		var attrs struct {
			ChannelID *string `json:"ChannelId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		cid := sv(attrs.ChannelID)
		if cid == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, mediapackageARN(sv(r.Region), acct.ID, "channels", cid))
		if !chSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert mp ep→channel: %w", err)
		}
	}
	return nil
}

// resolveMPV1AssetRefs wires each VOD asset to its packaging group, source S3
// bucket, and the IAM role used to ingest from S3.
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
	for _, r := range rows {
		var attrs struct {
			PackagingGroupID *string `json:"PackagingGroupId"`
			SourceArn        *string `json:"SourceArn"`
			SourceRoleArn    *string `json:"SourceRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if pg := sv(attrs.PackagingGroupID); pg != "" {
			tgtID := store.ResourceID("aws", acct.ID, mediapackageARN(region, acct.ID, "packaging-groups", pg))
			if pgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert mp asset→pg: %w", err)
				}
			}
		}
		if src := sv(attrs.SourceArn); src != "" {
			// Source ARN points to an S3 object — strip object key, leave bucket ARN.
			bktARN := src
			if i := strings.Index(src, "/"); i >= 0 && strings.HasPrefix(src, "arn:aws:s3:::") {
				bktARN = src[:i]
			}
			tgtID := store.ResourceID("aws", acct.ID, bktARN)
			if bktSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mp asset→s3: %w", err)
				}
			}
		}
		if role := sv(attrs.SourceRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mp asset→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveMPV1PackagingConfigToGroup wires each packaging-configuration to its
// packaging group via PackagingGroupID.
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
	for _, r := range rows {
		var attrs struct {
			PackagingGroupID *string `json:"PackagingGroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		pg := sv(attrs.PackagingGroupID)
		if pg == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, mediapackageARN(sv(r.Region), acct.ID, "packaging-groups", pg))
		if !pgSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert mp pcfg→pg: %w", err)
		}
	}
	return nil
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
