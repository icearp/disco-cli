package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// All Pinpoint child resources (channels, campaigns, segments, settings,
// event-streams) attach to the parent app via ApplicationID. Each resolver
// below rebuilds the canonical app ARN (mobiletargeting :apps/{id}) and
// FK-safe-emits an `attached-to` edge when the parent app is scanned.
//
// Templates are regional and keyed by name, with no per-app linkage;
// campaigns reference templates via TemplateConfiguration.*.Name, resolved
// through a (region, name) index over scanned templates.
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
		EdgeDecl{TypePinpointCampaign, TypePinpointVoiceTemplate, store.RelUses},
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
// app ARN; channels, settings, segments and event-streams all surface
// ApplicationID as a top-level field.
type pinpointAppIDOnly struct {
	ApplicationID *string `json:"ApplicationId"`
}

// linkPinpointApp emits r's `attached-to` edge to the app appID in r's region
// when that app was scanned; an empty appID emits nothing.
func linkPinpointApp(acct *account, st *store.Store, appSet map[string]bool, r store.Resource, appID string) error {
	if appID == "" {
		return nil
	}
	appResID := store.ResourceID("aws", acct.ID, pinpointAppARN(sv(r.Region), acct.ID, appID))
	if !appSet[appResID] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, appResID, store.RelAttachedTo, "directed", nil); err != nil {
		return fmt.Errorf("upsert pinpoint %s→app: %w", r.Type, err)
	}
	return nil
}

// linkPinpointChildrenToApps emits the `attached-to` edge from every row of
// types to its parent app, read from the row's ApplicationId. A row whose
// attributes do not decode costs only its own edge: the decode errors are
// returned joined after the other rows are linked.
func linkPinpointChildrenToApps(acct *account, st *store.Store, types []string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: types, Limit: util.AllResources,
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
	var decodeErrs []error
	for _, r := range rows {
		var attrs pinpointAppIDOnly
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", r.Type, r.NativeID, err))
			continue
		}
		if err := linkPinpointApp(acct, st, appSet, r, sv(attrs.ApplicationID)); err != nil {
			return err
		}
	}
	return errors.Join(decodeErrs...)
}

// resolvePinpointChannelApps emits `attached-to` edges from each channel
// row (and application-settings) to its parent Pinpoint app.
func resolvePinpointChannelApps(acct *account, st *store.Store) error {
	return linkPinpointChildrenToApps(acct, st, []string{
		TypePinpointADMChannel, TypePinpointAPNSChannel, TypePinpointAPNSSandboxChannel,
		TypePinpointAPNSVoipChannel, TypePinpointAPNSVoipSandboxChannel,
		TypePinpointBaiduChannel, TypePinpointEmailChannel, TypePinpointGCMChannel,
		TypePinpointSMSChannel, TypePinpointVoiceChannel,
		TypePinpointApplicationSettings,
	})
}

// resolvePinpointSegments wires each segment to its parent app.
func resolvePinpointSegments(acct *account, st *store.Store) error {
	return linkPinpointChildrenToApps(acct, st, []string{TypePinpointSegment})
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
		VoiceTemplate *pinpointTemplateRef `json:"VoiceTemplate"`
	} `json:"TemplateConfiguration"`
}

// pinpointTemplateKey names a template: names are unique per type within an
// account's region, and a campaign references a template of its own region.
type pinpointTemplateKey struct{ region, name string }

// pinpointTemplateIndex builds a (region, name) → resourceID map for one
// template type.
func pinpointTemplateIndex(acct *account, st *store.Store, rtype string) (map[pinpointTemplateKey]string, error) {
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[pinpointTemplateKey]string, len(rs))
	for _, r := range rs {
		if sv(r.Name) == "" {
			continue
		}
		m[pinpointTemplateKey{sv(r.Region), *r.Name}] = r.ID
	}
	return m, nil
}

// resolvePinpointCampaigns emits the campaign's parent-app edge, segment
// edge, and template edges (one per non-nil TemplateConfiguration slot).
// Segments rebuild the segment ARN from (region, acct, app, segmentID);
// templates resolve by (region, name) through a per-type index. A campaign
// whose attributes do not decode costs only its own edges: the decode errors
// are returned joined after the other campaigns are linked.
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
	tplIdx := map[string]map[pinpointTemplateKey]string{}
	for _, t := range []string{
		TypePinpointEmailTemplate, TypePinpointSmsTemplate,
		TypePinpointPushTemplate, TypePinpointInAppTemplate, TypePinpointVoiceTemplate,
	} {
		idx, err := pinpointTemplateIndex(acct, st, t)
		if err != nil {
			return err
		}
		tplIdx[t] = idx
	}
	var decodeErrs []error
	for _, c := range camps {
		var attrs pinpointCampaignAttrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypePinpointCampaign, c.NativeID, err))
			continue
		}
		region := sv(c.Region)
		appID := sv(attrs.ApplicationID)
		if err := linkPinpointApp(acct, st, appSet, c, appID); err != nil {
			return err
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
			TypePinpointVoiceTemplate: tc.VoiceTemplate,
		} {
			if ref == nil || sv(ref.Name) == "" {
				continue
			}
			tID, ok := tplIdx[tplType][pinpointTemplateKey{region, *ref.Name}]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(c.ID, tID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert pinpoint campaign→template: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// pinpointEventStreamAttrs covers the verbatim EventStream fields.
type pinpointEventStreamAttrs struct {
	ApplicationID        *string `json:"ApplicationId"`
	DestinationStreamArn *string `json:"DestinationStreamArn"`
	RoleArn              *string `json:"RoleArn"`
}

// resolvePinpointEventStreams wires each event-stream to (1) its parent
// app, (2) the scanned Kinesis stream or Firehose delivery stream its
// DestinationStreamArn names (both types are keyed by ARN), and (3) the IAM
// role. A stream whose attributes do not decode costs only its own edges: the
// decode errors are returned joined after the other streams are linked.
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
	var decodeErrs []error
	for _, s := range streams {
		var attrs pinpointEventStreamAttrs
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypePinpointEventStream, s.NativeID, err))
			continue
		}
		if err := linkPinpointApp(acct, st, appSet, s, sv(attrs.ApplicationID)); err != nil {
			return err
		}
		if dest := sv(attrs.DestinationStreamArn); dest != "" {
			dID := store.ResourceID("aws", acct.ID, dest)
			if kinSet[dID] || fhSet[dID] {
				if err := st.UpsertRelationship(s.ID, dID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert pinpoint event-stream→destination: %w", err)
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
	return errors.Join(decodeErrs...)
}
