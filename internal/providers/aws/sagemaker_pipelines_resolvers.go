package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveSageMakerProjectProduct,
		EdgeDecl{TypeSageMakerProject, TypeServiceCatalogProduct, store.RelUses},
	)
}

// resolveSageMakerProjectProduct wires each SageMaker project to the Service
// Catalog product its ServiceCatalogProvisioningDetails references. ProductID
// is a bare `prod-xxx` id; rebuild the catalog ARN per region+acct.
func resolveSageMakerProjectProduct(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSageMakerProject}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	prodSet, err := scannedIDSet(acct, st, TypeServiceCatalogProduct)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ServiceCatalogProvisioningDetails *struct {
				ProductID *string `json:"ProductId"`
			} `json:"ServiceCatalogProvisioningDetails"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ServiceCatalogProvisioningDetails == nil {
			continue
		}
		pid := sv(attrs.ServiceCatalogProvisioningDetails.ProductID)
		if pid == "" {
			continue
		}
		pARN := fmt.Sprintf("arn:aws:catalog:%s:%s:product/%s", sv(r.Region), acct.ID, pid)
		tgt := store.ResourceID("aws", acct.ID, TypeServiceCatalogProduct, pARN)
		if !prodSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert sagemaker project→product: %w", err)
		}
	}
	return nil
}
