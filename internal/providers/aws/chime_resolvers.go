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
		resolveChimeAppInstanceChildren,
		EdgeDecl{TypeChimeAppInstanceBot, TypeChimeAppInstance, store.RelAttachedTo},
		EdgeDecl{TypeChimeAppInstanceUser, TypeChimeAppInstance, store.RelAttachedTo},
		EdgeDecl{TypeChimeChannelFlow, TypeChimeAppInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveChimeVoiceProfileDomain,
		EdgeDecl{TypeChimeVoiceProfile, TypeChimeVoiceProfileDomain, store.RelAttachedTo},
		EdgeDecl{TypeChimeVoiceProfileDomain, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveChimeSipMediaApplicationLambda,
		EdgeDecl{TypeChimeSipMediaApplication, TypeLambdaFunction, store.RelUses},
	)
	registerResolver(
		resolveChimeSipRuleTargets,
		EdgeDecl{TypeChimeSipRule, TypeChimeSipMediaApplication, store.RelRoutesTo},
	)
	registerResolver(
		resolveChimeVoiceConnectorGroupItems,
		EdgeDecl{TypeChimeVoiceConnectorGroup, TypeChimeVoiceConnector, store.RelRoutesTo},
	)
}

// chimeRowsByAttrID indexes the stored rows of typ by key(attrs), skipping
// rows whose key is empty. Rows whose attributes do not decode are skipped and
// their errors returned joined.
func chimeRowsByAttrID[A any](acct *account, st *store.Store, typ string, key func(a A) string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{typ}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	var decodeErrs []error
	for _, r := range rows {
		var a A
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s: %w", typ, r.NativeID, err))
			continue
		}
		if k := key(a); k != "" {
			idx[k] = r.ID
		}
	}
	return idx, errors.Join(decodeErrs...)
}

// resolveChimeSipRuleTargets wires each SIP rule to the SIP media applications
// it routes calls to. A target names the application by id and region, so the
// applications are indexed by the SipMediaApplicationId and AwsRegion in their
// attributes. FK-safe; a target in an unscanned region gets no edge.
func resolveChimeSipRuleTargets(acct *account, st *store.Store) error {
	type smaAttrs struct {
		SipMediaApplicationID *string `json:"SipMediaApplicationId"`
		AwsRegion             *string `json:"AwsRegion"`
	}
	smaKey := func(region, id string) string {
		if region == "" || id == "" {
			return ""
		}
		return region + "/" + id
	}
	smaByKey, err := chimeRowsByAttrID(acct, st, TypeChimeSipMediaApplication, func(a smaAttrs) string {
		return smaKey(sv(a.AwsRegion), sv(a.SipMediaApplicationID))
	})
	if len(smaByKey) == 0 {
		return err
	}
	errs := []error{err}

	rules, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeSipRule}, Limit: util.AllResources,
	})
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, r := range rules {
		var a struct {
			TargetApplications []smaAttrs `json:"TargetApplications"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			errs = append(errs, fmt.Errorf("decode %s %s: %w", TypeChimeSipRule, r.NativeID, err))
			continue
		}
		for _, t := range a.TargetApplications {
			smaID, ok := smaByKey[smaKey(sv(t.AwsRegion), sv(t.SipMediaApplicationID))]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, smaID, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert chime sip-rule→sip-media-application: %w", err)
			}
		}
	}
	return errors.Join(errs...)
}

// resolveChimeVoiceConnectorGroupItems wires each voice connector group to the
// voice connectors it routes inbound calls to. An item names the connector by
// id only, and a group may hold connectors from other regions, so connectors
// are indexed by the VoiceConnectorId in their attributes, account-wide. FK-safe.
func resolveChimeVoiceConnectorGroupItems(acct *account, st *store.Store) error {
	type vcRef struct {
		VoiceConnectorID *string `json:"VoiceConnectorId"`
	}
	vcByID, err := chimeRowsByAttrID(acct, st, TypeChimeVoiceConnector, func(a vcRef) string {
		return sv(a.VoiceConnectorID)
	})
	if len(vcByID) == 0 {
		return err
	}
	errs := []error{err}

	groups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeVoiceConnectorGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, g := range groups {
		var a struct {
			VoiceConnectorItems []vcRef `json:"VoiceConnectorItems"`
		}
		if err := json.Unmarshal([]byte(g.AttributesJSON), &a); err != nil {
			errs = append(errs, fmt.Errorf("decode %s %s: %w", TypeChimeVoiceConnectorGroup, g.NativeID, err))
			continue
		}
		for _, it := range a.VoiceConnectorItems {
			vcID, ok := vcByID[sv(it.VoiceConnectorID)]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(g.ID, vcID, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert chime voice-connector-group→voice-connector: %w", err)
			}
		}
	}
	return errors.Join(errs...)
}

// resolveChimeAppInstanceChildren wires bots, users and channel flows to their
// app-instance. Bot/user ARNs embed the app-instance (…/bot/{id}, …/user/{id});
// channel flows carry the owning ARN in the stored wrapper. FK-safe.
func resolveChimeAppInstanceChildren(acct *account, st *store.Store) error {
	aiSet, err := scannedIDSet(acct, st, TypeChimeAppInstance)
	if err != nil {
		return err
	}
	if len(aiSet) == 0 {
		return nil
	}
	edge := func(srcID, aiARN string) error {
		if aiARN == "" {
			return nil
		}
		tgtID := store.ResourceID("aws", acct.ID, aiARN)
		if !aiSet[tgtID] {
			return nil
		}
		if err := st.UpsertRelationship(srcID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert chime child→app-instance: %w", err)
		}
		return nil
	}

	for _, pair := range []struct{ typ, sep string }{
		{TypeChimeAppInstanceBot, "/bot/"},
		{TypeChimeAppInstanceUser, "/user/"},
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{pair.typ}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if i := strings.LastIndex(r.NativeID, pair.sep); i >= 0 {
				if err := edge(r.ID, r.NativeID[:i]); err != nil {
					return err
				}
			}
		}
	}

	flows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeChannelFlow}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range flows {
		var attrs struct {
			AppInstanceArn *string `json:"appInstanceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := edge(r.ID, sv(attrs.AppInstanceArn)); err != nil {
			return err
		}
	}
	return nil
}

// resolveChimeVoiceProfileDomain wires voice profiles to their domain (by the
// domain id each profile carries) and each domain to its KMS key (from the
// enriched GetVoiceProfileDomain body). FK-safe.
func resolveChimeVoiceProfileDomain(acct *account, st *store.Store) error {
	domains, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeVoiceProfileDomain}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(domains) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	// Index domains by their VoiceProfileDomainId → resource id (shape-agnostic).
	domainByID := make(map[string]string, len(domains))
	for _, d := range domains {
		var a struct {
			VoiceProfileDomainID              *string `json:"VoiceProfileDomainId"`
			ServerSideEncryptionConfiguration *struct {
				KmsKeyArn *string `json:"KmsKeyArn"`
			} `json:"ServerSideEncryptionConfiguration"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &a); err != nil {
			continue
		}
		if id := sv(a.VoiceProfileDomainID); id != "" {
			domainByID[id] = d.ID
		}
		if a.ServerSideEncryptionConfiguration != nil {
			if ref := sv(a.ServerSideEncryptionConfiguration.KmsKeyArn); ref != "" {
				if keyID, ok := kmsIdx.resolveKMSKeyID(ref, sv(d.Region), acct.ID); ok {
					if err := st.UpsertRelationship(d.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert chime voice-profile-domain→kms: %w", err)
					}
				}
			}
		}
	}

	profiles, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeVoiceProfile}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, p := range profiles {
		var a struct {
			VoiceProfileDomainID *string `json:"VoiceProfileDomainId"`
		}
		if err := json.Unmarshal([]byte(p.AttributesJSON), &a); err != nil {
			continue
		}
		domID, ok := domainByID[sv(a.VoiceProfileDomainID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(p.ID, domID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert chime voice-profile→domain: %w", err)
		}
	}
	return nil
}

// resolveChimeSipMediaApplicationLambda wires each SIP media application to the
// Lambda function(s) backing its endpoints. FK-safe.
func resolveChimeSipMediaApplicationLambda(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeSipMediaApplication}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	lambdaSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var a struct {
			Endpoints []struct {
				LambdaArn *string `json:"LambdaArn"`
			} `json:"Endpoints"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, ep := range a.Endpoints {
			arn := sv(ep.LambdaArn)
			if arn == "" || seen[arn] {
				continue
			}
			seen[arn] = true
			tgtID := store.ResourceID("aws", acct.ID, arn)
			if !lambdaSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert chime sip-media-application→lambda: %w", err)
			}
		}
	}
	return nil
}
