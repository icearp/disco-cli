package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		noopConnectAccessHierarchy,
		EdgeDecl{TypeConnectSecurityProfile, TypeConnectSecurityProfilePermission, store.RelContains},
		EdgeDecl{TypeConnectInstance, TypeConnectLambdaFunction, store.RelContains},
		EdgeDecl{TypeConnectRoutingProfile, TypeConnectRoutingProfileQueue, store.RelContains},
	)
	registerResolver(
		resolveConnectLambdaFunctionTarget,
		EdgeDecl{TypeConnectLambdaFunction, TypeLambdaFunction, store.RelUses},
	)
	registerResolver(
		resolveConnectRoutingProfileQueueTarget,
		EdgeDecl{TypeConnectRoutingProfileQueue, TypeConnectQueue, store.RelUses},
	)
}

// noopConnectAccessHierarchy only declares the contains edges that
// scanConnectAccess records through childFanOut.
func noopConnectAccessHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveConnectLambdaFunctionTarget links an allowlist entry to the Lambda
// function its FunctionArn names; a qualified ARN (alias or version) links to
// its unqualified function. A function that is not stored, such as one in
// another account, gets no edge.
func resolveConnectLambdaFunctionTarget(acct *account, st *store.Store) error {
	return connectARNFieldEdge(acct, st, TypeConnectLambdaFunction, TypeLambdaFunction, func(raw []byte) (string, error) {
		var attrs connectLambdaFunctionAttrs
		err := json.Unmarshal(raw, &attrs)
		return lambdaStripQualifier(attrs.FunctionArn), err
	})
}

// resolveConnectRoutingProfileQueueTarget links a routing profile's queue
// config to the queue its QueueArn names.
func resolveConnectRoutingProfileQueueTarget(acct *account, st *store.Store) error {
	return connectARNFieldEdge(acct, st, TypeConnectRoutingProfileQueue, TypeConnectQueue, func(raw []byte) (string, error) {
		var attrs struct {
			QueueArn *string `json:"QueueArn"`
		}
		err := json.Unmarshal(raw, &attrs)
		return sv(attrs.QueueArn), err
	})
}

// connectARNFieldEdge emits a uses edge from each srcType row to the stored
// tgtType row whose NativeID is the ARN arnOf reads from its attributes. A
// row that fails to decode is reported at the end, so that it costs the other
// rows none of their edges.
func connectARNFieldEdge(acct *account, st *store.Store, srcType, tgtType string, arnOf func([]byte) (string, error)) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{srcType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tgtSet, err := scannedIDSet(acct, st, tgtType)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		arn, err := arnOf([]byte(r.AttributesJSON))
		if err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", srcType, r.NativeID, err))
			continue
		}
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !tgtSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert %s→%s: %w", srcType, tgtType, err)
		}
	}
	return errors.Join(decodeErrs...)
}
