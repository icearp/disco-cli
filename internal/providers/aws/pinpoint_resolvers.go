package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// All Pinpoint child resources (channels, campaigns, segments, settings,
// event-streams) attach to the parent app via ApplicationID. Each resolver
// below rebuilds the canonical app ARN (mobiletargeting :apps/{id}) and
// FK-safe-emits an `attached-to` edge when the parent app is scanned.
//
// Templates are name-keyed account-wide with no per-app linkage; campaigns
// reference templates via TemplateConfiguration.*.Name, resolved through a
// name-keyed index over scanned templates.
func init() {
	registerResolver(
		resolvePinpointChannelApps,
		EdgeDecl{TypePinpointADMChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointAPNSChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointAPNSSandboxChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointAPNSVoipChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointAPNSVoipSandboxChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointBaiduChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointEmailChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointGCMChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointSMSChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointVoiceChannel, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointApplicationSettings, TypePinpointApp, store.RelAttachedTo},
	)
	registerResolver(
		resolvePinpointCampaigns,
		EdgeDecl{TypePinpointCampaign, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointCampaign, TypePinpointSegment, store.RelUses},
		EdgeDecl{TypePinpointCampaign, TypePinpointEmailTemplate, store.RelUses},
		EdgeDecl{TypePinpointCampaign, TypePinpointSmsTemplate, store.RelUses},
		EdgeDecl{TypePinpointCampaign, TypePinpointPushTemplate, store.RelUses},
		EdgeDecl{TypePinpointCampaign, TypePinpointInAppTemplate, store.RelUses},
	)
	registerResolver(
		resolvePinpointSegments,
		EdgeDecl{TypePinpointSegment, TypePinpointApp, store.RelAttachedTo},
	)
	registerResolver(
		resolvePinpointEventStreams,
		EdgeDecl{TypePinpointEventStream, TypePinpointApp, store.RelAttachedTo},
		EdgeDecl{TypePinpointEventStream, TypeKinesisStream, store.RelUses},
		EdgeDecl{TypePinpointEventStream, TypeFirehoseDeliveryStream, store.RelUses},
		EdgeDecl{TypePinpointEventStream, TypeIAMRole, store.RelAssumes},
	)
}

// pinpointAppARN builds the canonical Pinpoint app ARN — mirrors
// `pinpointARN` in pinpoint_scanners.go for the parent app itself
// (no kind suffix under `apps/{id}`).
func pinpointAppARN(region, acctID, appID string) string {
	return fmt.Sprintf("arn:aws:mobiletargeting:%s:%s:apps/%s", region, acctID, appID)
}

// pinpointAppIDOnly carries only the field needed to recover the parent
// app ARN; channels, settings, and event-streams all surface ApplicationID
// as a top-level field.
type pinpointAppIDOnly struct {
	ApplicationID *string `json:"ApplicationId"`
}

// resolvePinpointChannelApps emits `attached-to` edges from each channel
// row (and application-settings) to its parent Pinpoint app. Walks every
// channel type plus the singleton settings type in one pass, sharing the
// scanned-app id set.
func resolvePinpointChannelApps(acct *account, st *store.Store) error {
	childTypes := []string{
		TypePinpointADMChannel, TypePinpointAPNSChannel, TypePinpointAPNSSandboxChannel,
		TypePinpointAPNSVoipChannel, TypePinpointAPNSVoipSandboxChannel,
		TypePinpointBaiduChannel, TypePinpointEmailChannel, TypePinpointGCMChannel,
		TypePinpointSMSChannel, TypePinpointVoiceChannel,
		TypePinpointApplicationSettings,
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: childTypes, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypePinpointApp)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs pinpointAppIDOnly
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		appID := sv(attrs.ApplicationID)
		if appID == "" {
			continue
		}
		appResID := store.ResourceID("aws", acct.ID,
			pinpointAppARN(sv(r.Region), acct.ID, appID))

		if !appSet[appResID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, appResID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert pinpoint child→app: %w", err)
		}
	}
	return nil
}

// pinpointTemplateRef is the embedded `Template` block under each slot in
// TemplateConfiguration. Only Name is used (templates resolved by name).
type pinpointTemplateRef struct {
	Name *string `json:"Name"`
}

// pinpointCampaignAttrs mirrors the verbatim CampaignResponse fields.
type pinpointCampaignAttrs struct {
	ApplicationID         *string `json:"ApplicationId"`
	SegmentID             *string `json:"SegmentId"`
	TemplateConfiguration *struct {
		EmailTemplate *pinpointTemplateRef `json:"EmailTemplate"`
		SMSTemplate   *pinpointTemplateRef `json:"SMSTemplate"`
		PushTemplate  *pinpointTemplateRef `json:"PushTemplate"`
		InAppTemplate *pinpointTemplateRef `json:"InAppTemplate"`
		// VoiceTemplate intentionally skipped — not scanned (see
		// scanPinpointTemplates VOICE branch).
	} `json:"TemplateConfiguration"`
}

// pinpointTemplateNameIndex builds a name → resourceID map for one
// template type. Templates are account-wide, so no region segmentation.
func pinpointTemplateNameIndex(acct *account, st *store.Store, rtype string) (map[string]string, error) {
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rs))
	for _, r := range rs {
		if r.Name == nil || *r.Name == "" {
			continue
		}
		m[*r.Name] = r.ID
	}
	return m, nil
}

// resolvePinpointCampaigns emits the campaign's parent-app edge, segment
// edge, and template edges (one per non-nil TemplateConfiguration slot).
// Segments rebuild the segment ARN from (region, acct, app, segmentID);
// templates resolve by name through a per-type index.
func resolvePinpointCampaigns(acct *account, st *store.Store) error {
	camps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypePinpointCampaign},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(camps) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypePinpointApp)
	if err != nil {
		return err
	}
	segSet, err := scannedIDSet(acct, st, TypePinpointSegment)
	if err != nil {
		return err
	}
	tplIdx := map[string]map[string]string{}
	for _, t := range []string{
		TypePinpointEmailTemplate, TypePinpointSmsTemplate,
		TypePinpointPushTemplate, TypePinpointInAppTemplate,
	} {
		idx, err := pinpointTemplateNameIndex(acct, st, t)
		if err != nil {
			return err
		}
		tplIdx[t] = idx
	}
	for _, c := range camps {
		var attrs pinpointCampaignAttrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(c.Region)
		appID := sv(attrs.ApplicationID)
		if appID != "" {
			ar := store.ResourceID("aws", acct.ID, pinpointAppARN(region, acct.ID, appID))
			if appSet[ar] {
				if err := st.UpsertRelationship(c.ID, ar, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert pinpoint campaign→app: %w", err)
				}
			}
		}
		if seg := sv(attrs.SegmentID); seg != "" && appID != "" {
			segARN := pinpointARN(region, acct.ID, "segments/"+seg, appID)
			segID := store.ResourceID("aws", acct.ID, segARN)
			if segSet[segID] {
				if err := st.UpsertRelationship(c.ID, segID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert pinpoint campaign→segment: %w", err)
				}
			}
		}
		if attrs.TemplateConfiguration == nil {
			continue
		}
		tc := attrs.TemplateConfiguration
		for tplType, ref := range map[string]*pinpointTemplateRef{
			TypePinpointEmailTemplate: tc.EmailTemplate,
			TypePinpointSmsTemplate:   tc.SMSTemplate,
			TypePinpointPushTemplate:  tc.PushTemplate,
			TypePinpointInAppTemplate: tc.InAppTemplate,
		} {
			if ref == nil || sv(ref.Name) == "" {
				continue
			}
			tID, ok := tplIdx[tplType][*ref.Name]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(c.ID, tID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert pinpoint campaign→template: %w", err)
			}
		}
	}
	return nil
}

// resolvePinpointSegments wires each segment to its parent app. Segments
// have their own ARN as NativeID, so we read ApplicationID from attrs.
func resolvePinpointSegments(acct *account, st *store.Store) error {
	segs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypePinpointSegment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypePinpointApp)
	if err != nil {
		return err
	}
	for _, s := range segs {
		var attrs pinpointAppIDOnly
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		appID := sv(attrs.ApplicationID)
		if appID == "" {
			continue
		}
		appResID := store.ResourceID("aws", acct.ID,
			pinpointAppARN(sv(s.Region), acct.ID, appID))

		if !appSet[appResID] {
			continue
		}
		if err := st.UpsertRelationship(s.ID, appResID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert pinpoint segment→app: %w", err)
		}
	}
	return nil
}

// pinpointEventStreamAttrs covers the verbatim EventStream fields.
type pinpointEventStreamAttrs struct {
	ApplicationID        *string `json:"ApplicationId"`
	DestinationStreamArn *string `json:"DestinationStreamArn"`
	RoleArn              *string `json:"RoleArn"`
}

// resolvePinpointEventStreams wires each event-stream to (1) its parent
// app, (2) the Kinesis stream OR Firehose delivery-stream destination
// (DestinationStreamArn ARN prefix discriminates), and (3) the IAM role.
func resolvePinpointEventStreams(acct *account, st *store.Store) error {
	streams, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypePinpointEventStream},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(streams) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypePinpointApp)
	if err != nil {
		return err
	}
	kinSet, err := scannedIDSet(acct, st, TypeKinesisStream)
	if err != nil {
		return err
	}
	fhSet, err := scannedIDSet(acct, st, TypeFirehoseDeliveryStream)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, s := range streams {
		var attrs pinpointEventStreamAttrs
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(s.Region)
		if appID := sv(attrs.ApplicationID); appID != "" {
			ar := store.ResourceID("aws", acct.ID, pinpointAppARN(region, acct.ID, appID))
			if appSet[ar] {
				if err := st.UpsertRelationship(s.ID, ar, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert pinpoint event-stream→app: %w", err)
				}
			}
		}
		if dest := sv(attrs.DestinationStreamArn); dest != "" {
			// arn:aws:kinesis:... → Kinesis stream; arn:aws:firehose:... → Firehose.
			var targetType string
			switch {
			case len(dest) > 14 && dest[:14] == "arn:aws:kinesis":
				targetType = TypeKinesisStream
			case len(dest) > 15 && dest[:15] == "arn:aws:firehose":
				targetType = TypeFirehoseDeliveryStream
			}
			if targetType != "" {
				dID := store.ResourceID("aws", acct.ID, dest)
				ok := false
				switch targetType {
				case TypeKinesisStream:
					ok = kinSet[dID]
				case TypeFirehoseDeliveryStream:
					ok = fhSet[dID]
				}
				if ok {
					if err := st.UpsertRelationship(s.ID, dID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert pinpoint event-stream→destination: %w", err)
					}
				}
			}
		}
		if roleArn := sv(attrs.RoleArn); roleArn != "" {
			rID := store.ResourceID("aws", acct.ID, roleArn)
			if roleSet[rID] {
				if err := st.UpsertRelationship(s.ID, rID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert pinpoint event-stream→role: %w", err)
				}
			}
		}
	}
	return nil
}
