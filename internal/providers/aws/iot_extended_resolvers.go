package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveIoTSecurityProfileRefs,
		EdgeDecl{TypeIoTSecurityProfile, TypeIoTCustomMetric, store.RelUses},
		EdgeDecl{TypeIoTSecurityProfile, TypeIoTDimension, store.RelUses},
	)
}

// iotByRegionName builds (region, name) → resource ID lookup for IoT defender
// resources keyed by name.
func iotByRegionName(acct *account, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range rows {
		if r.Name != nil && *r.Name != "" {
			out[sv(r.Region)+"|"+*r.Name] = r.ID
		}
	}
	return out, nil
}

// resolveIoTSecurityProfileRefs wires security-profile → custom-metric (by
// Behaviors[].Metric name) and dimension (by Behaviors[].MetricDimension.
// DimensionName + AdditionalMetricsToRetainV2[].MetricDimension.DimensionName).
// Built-in standard metric names that don't match a scanned custom-metric are
// skipped via FK-safe lookup.
func resolveIoTSecurityProfileRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTSecurityProfile}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	cmIdx, err := iotByRegionName(acct, st, TypeIoTCustomMetric)
	if err != nil {
		return err
	}
	dimIdx, err := iotByRegionName(acct, st, TypeIoTDimension)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Behaviors []struct {
				Metric          *string `json:"Metric"`
				MetricDimension *struct {
					DimensionName *string `json:"DimensionName"`
				} `json:"MetricDimension"`
			} `json:"Behaviors"`
			AdditionalMetricsToRetainV2 []struct {
				MetricDimension *struct {
					DimensionName *string `json:"DimensionName"`
				} `json:"MetricDimension"`
			} `json:"AdditionalMetricsToRetainV2"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		emitDim := func(name string) error {
			if name == "" {
				return nil
			}
			if tgtID, ok := dimIdx[region+"|"+name]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert iot sp→dim: %w", err)
				}
			}
			return nil
		}
		for _, b := range attrs.Behaviors {
			if m := sv(b.Metric); m != "" {
				if tgtID, ok := cmIdx[region+"|"+m]; ok {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert iot sp→cm: %w", err)
					}
				}
			}
			if b.MetricDimension != nil {
				if err := emitDim(sv(b.MetricDimension.DimensionName)); err != nil {
					return err
				}
			}
		}
		for _, a := range attrs.AdditionalMetricsToRetainV2 {
			if a.MetricDimension != nil {
				if err := emitDim(sv(a.MetricDimension.DimensionName)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
