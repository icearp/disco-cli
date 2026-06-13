package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveMediaTailorChannelPolicyToChannel,
		EdgeDecl{TypeMediaTailorChannelPolicy, TypeMediaTailorChannel, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaTailorSourcesToSourceLocation,
		EdgeDecl{TypeMediaTailorLiveSource, TypeMediaTailorSourceLocation, store.RelAttachedTo},
		EdgeDecl{TypeMediaTailorVodSource, TypeMediaTailorSourceLocation, store.RelAttachedTo},
	)
}

func mtSourceLocationARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:mediatailor:%s:%s:sourceLocation/%s", region, acct, name)
}

// resolveMediaTailorChannelPolicyToChannel wires channel-policy → channel by
// stripping the trailing `/policy` segment. Channel ARN shape:
// `arn:aws:mediatailor:r:a:channel/{name}`; policy NativeID:
// `arn:aws:mediatailor:r:a:channel/{name}/policy`.
func resolveMediaTailorChannelPolicyToChannel(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMediaTailorChannelPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	chSet, err := scannedIDSet(acct, st, TypeMediaTailorChannel)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeMediaTailorChannel, parent)
		if !chSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert mt channel-policy→channel: %w", err)
		}
	}
	return nil
}

// resolveMediaTailorSourcesToSourceLocation wires live-source and vod-source
// to their parent source-location via SourceLocationName.
func resolveMediaTailorSourcesToSourceLocation(acct *account, st *store.Store) error {
	slSet, err := scannedIDSet(acct, st, TypeMediaTailorSourceLocation)
	if err != nil {
		return err
	}
	for _, t := range []string{TypeMediaTailorLiveSource, TypeMediaTailorVodSource} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				SourceLocationName *string `json:"SourceLocationName"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			n := sv(attrs.SourceLocationName)
			if n == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeMediaTailorSourceLocation, mtSourceLocationARN(sv(r.Region), acct.ID, n))
			if !slSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert mt %s→source-location: %w", t, err)
			}
		}
	}
	return nil
}
