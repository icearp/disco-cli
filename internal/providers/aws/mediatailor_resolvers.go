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
		resolveMediaTailorChannelPolicyToChannel,
		EdgeDecl{TypeMediaTailorChannelPolicy, TypeMediaTailorChannel, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaTailorSourcesToSourceLocation,
		EdgeDecl{TypeMediaTailorLiveSource, TypeMediaTailorSourceLocation, store.RelAttachedTo},
		EdgeDecl{TypeMediaTailorVodSource, TypeMediaTailorSourceLocation, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaTailorFunctionUses,
		EdgeDecl{TypeMediaTailorPlaybackConfiguration, TypeMediaTailorFunction, store.RelUses},
		EdgeDecl{TypeMediaTailorFunction, TypeMediaTailorFunction, store.RelUses},
	)
}

func mtSourceLocationARN(partition, region, acct, name string) string {
	return arnFor(partition, "mediatailor", region, acct, "sourceLocation/"+name)
}

// resolveMediaTailorChannelPolicyToChannel wires channel-policy → channel by
// stripping the trailing `/policy` segment. Channel ARN:
// `arn:{partition}:mediatailor:{r}:{a}:channel/{name}`; policy NativeID adds `/policy`.
func resolveMediaTailorChannelPolicyToChannel(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaTailorChannelPolicy}, Limit: util.AllResources,
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
		tgtID := store.ResourceID("aws", acct.ID, parent)
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
// to their parent source-location via SourceLocationName. A row whose
// attributes do not decode is reported and skipped; its siblings still resolve.
func resolveMediaTailorSourcesToSourceLocation(acct *account, st *store.Store) error {
	slSet, err := scannedIDSet(acct, st, TypeMediaTailorSourceLocation)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range []string{TypeMediaTailorLiveSource, TypeMediaTailorVodSource} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, r := range rows {
			var attrs struct {
				SourceLocationName *string `json:"SourceLocationName"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				errs = append(errs, fmt.Errorf("decode %s %s: %w", t, r.NativeID, err))
				continue
			}
			n := sv(attrs.SourceLocationName)
			if n == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, mtSourceLocationARN(acct.partition(), sv(r.Region), acct.ID, n))
			if !slSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return errors.Join(append(errs, fmt.Errorf("upsert mt %s→source-location: %w", t, err))...)
			}
		}
	}
	return errors.Join(errs...)
}

// mtFunctionKey names a function the way its referrers do: by bare
// FunctionId, which is unique only within one region of the account.
type mtFunctionKey struct{ region, id string }

// mtFunctionAttrs is the part of a stored function row the resolver reads:
// its own id, and the child function each SEQUENTIAL_EXECUTOR step runs.
type mtFunctionAttrs struct {
	FunctionID                      *string `json:"FunctionId"`
	SequentialExecutorConfiguration *struct {
		FunctionList []struct {
			FunctionID *string `json:"FunctionId"`
		} `json:"FunctionList"`
	} `json:"SequentialExecutorConfiguration"`
}

func (a mtFunctionAttrs) stepIDs() []string {
	if a.SequentialExecutorConfiguration == nil {
		return nil
	}
	var ids []string
	for _, s := range a.SequentialExecutorConfiguration.FunctionList {
		ids = append(ids, sv(s.FunctionID))
	}
	return ids
}

// resolveMediaTailorFunctionUses wires each playback configuration to the
// functions its FunctionMapping runs at lifecycle hooks, and each
// SEQUENTIAL_EXECUTOR function to the functions its steps run. Both refs are
// a bare FunctionId and the service reference publishes no function ARN
// format, so a target is the scanned function row carrying that FunctionId in
// the referrer's region. A row whose attributes do not decode is reported and
// skipped; its siblings still resolve.
func resolveMediaTailorFunctionUses(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaTailorFunction}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(fns) == 0 {
		return nil
	}
	var errs []error
	byKey := make(map[mtFunctionKey]string, len(fns))
	steps := make(map[string][]mtFunctionKey)
	for _, f := range fns {
		var attrs mtFunctionAttrs
		if err := json.Unmarshal([]byte(f.AttributesJSON), &attrs); err != nil {
			errs = append(errs, fmt.Errorf("decode mediatailor function %s: %w", f.NativeID, err))
			continue
		}
		region := sv(f.Region)
		if id := sv(attrs.FunctionID); id != "" {
			byKey[mtFunctionKey{region, id}] = f.ID
		}
		for _, id := range attrs.stepIDs() {
			steps[f.ID] = append(steps[f.ID], mtFunctionKey{region, id})
		}
	}
	link := func(fromID string, k mtFunctionKey) error {
		toID, ok := byKey[k]
		if !ok {
			return nil
		}
		if err := st.UpsertRelationship(fromID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert mt →function %s: %w", k.id, err)
		}
		return nil
	}
	for fromID, keys := range steps {
		for _, k := range keys {
			if err := link(fromID, k); err != nil {
				return errors.Join(append(errs, err)...)
			}
		}
	}

	pcs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaTailorPlaybackConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, pc := range pcs {
		var attrs struct {
			FunctionMapping map[string]string `json:"FunctionMapping"`
		}
		if err := json.Unmarshal([]byte(pc.AttributesJSON), &attrs); err != nil {
			errs = append(errs, fmt.Errorf("decode mediatailor playback-configuration %s: %w", pc.NativeID, err))
			continue
		}
		for _, id := range attrs.FunctionMapping {
			if err := link(pc.ID, mtFunctionKey{sv(pc.Region), id}); err != nil {
				return errors.Join(append(errs, err)...)
			}
		}
	}
	return errors.Join(errs...)
}
