package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveConnectChildrenToInstance,
		EdgeDecl{TypeConnectAgentStatus, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectApprovedOrigin, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectContactFlow, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectContactFlowModule, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectDataTable, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectEmailAddress, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectEvaluationForm, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectHoursOfOperation, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectNotification, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectPredefinedAttribute, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectPrompt, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectRule, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectSecurityKey, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectSecurityProfile, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectTaskTemplate, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectUserHierarchyGroup, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectUserHierarchyStructure, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectView, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectViewVersion, TypeConnectInstance, store.RelAttachedTo},
		EdgeDecl{TypeConnectWorkspace, TypeConnectInstance, store.RelAttachedTo},
	)
	registerResolver(resolveConnectViewVersionToView,
		EdgeDecl{TypeConnectViewVersion, TypeConnectView, store.RelAttachedTo},
	)
	registerResolver(resolveConnectDataTableChildrenToTable,
		EdgeDecl{TypeConnectDataTableAttribute, TypeConnectDataTable, store.RelAttachedTo},
		EdgeDecl{TypeConnectDataTableRecord, TypeConnectDataTable, store.RelAttachedTo},
	)
	registerResolver(resolveConnectInstanceServiceRole,
		EdgeDecl{TypeConnectInstance, TypeIAMRole, store.RelAssumes},
	)
}

// resolveConnectInstanceServiceRole wires each Connect instance to its
// service-linked IAM role (DescribeInstanceOutput.Instance.ServiceRole).
// FK-safe.
func resolveConnectInstanceServiceRole(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeConnectInstance}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Instance *struct {
				ServiceRole *string `json:"ServiceRole"`
			} `json:"Instance"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Instance == nil {
			continue
		}
		arn := sv(attrs.Instance.ServiceRole)
		if arn == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
		if !roleSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert connect instance→role: %w", err)
		}
	}
	return nil
}

// connectInstanceARNFromChild rebuilds the parent instance ARN
// `arn:aws:connect:r:a:instance/{id}` from any per-instance child NativeID
// of shape `…:instance/{id}/<kind>/...`.
func connectInstanceARNFromChild(arn string) string {
	const prefix = "instance/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

func resolveConnectChildrenToInstance(acct *account, st *store.Store) error {
	instSet, err := scannedIDSet(acct, st, TypeConnectInstance)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeConnectAgentStatus,
		TypeConnectApprovedOrigin,
		TypeConnectContactFlow,
		TypeConnectContactFlowModule,
		TypeConnectDataTable,
		TypeConnectEmailAddress,
		TypeConnectEvaluationForm,
		TypeConnectHoursOfOperation,
		TypeConnectNotification,
		TypeConnectPredefinedAttribute,
		TypeConnectPrompt,
		TypeConnectRule,
		TypeConnectSecurityKey,
		TypeConnectSecurityProfile,
		TypeConnectTaskTemplate,
		TypeConnectUserHierarchyGroup,
		TypeConnectUserHierarchyStructure,
		TypeConnectView,
		TypeConnectViewVersion,
		TypeConnectWorkspace,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := connectInstanceARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeConnectInstance, parent)
			if !instSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert connect %s→instance: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveConnectViewVersionToView wires each view-version to its parent view
// by stripping the trailing `/<verID>` from the version's NativeID.
func resolveConnectViewVersionToView(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeConnectViewVersion}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	viewSet, err := scannedIDSet(acct, st, TypeConnectView)
	if err != nil {
		return err
	}
	for _, r := range rows {
		// Scanner builds NativeID as `…:instance/{i}/view-version/{viewID}/{verID}`.
		// Strip trailing `/{verID}` then swap segment to `view`.
		last := strings.LastIndexByte(r.NativeID, '/')
		if last < 0 {
			continue
		}
		viewVerARN := r.NativeID[:last] // …:instance/{i}/view-version/{viewID}
		viewARN := strings.Replace(viewVerARN, "/view-version/", "/view/", 1)
		tgtID := store.ResourceID("aws", acct.ID, TypeConnectView, viewARN)
		if !viewSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert connect view-version→view: %w", err)
		}
	}
	return nil
}

// resolveConnectDataTableChildrenToTable wires data-table-attribute and
// data-table-record rows to their parent data-table by stripping the trailing
// `/<id>` and swapping the kind segment.
func resolveConnectDataTableChildrenToTable(acct *account, st *store.Store) error {
	tableSet, err := scannedIDSet(acct, st, TypeConnectDataTable)
	if err != nil {
		return err
	}
	for _, c := range []struct {
		ctype string
		seg   string
	}{
		{TypeConnectDataTableAttribute, "/data-table-attribute/"},
		{TypeConnectDataTableRecord, "/data-table-record/"},
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{c.ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			i := strings.Index(r.NativeID, c.seg)
			if i < 0 {
				continue
			}
			tail := r.NativeID[i+len(c.seg):]
			end := strings.IndexByte(tail, '/')
			if end < 0 {
				continue
			}
			tableID := tail[:end]
			tableARN := r.NativeID[:i] + "/data-table/" + tableID
			tgtID := store.ResourceID("aws", acct.ID, TypeConnectDataTable, tableARN)
			if !tableSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert connect %s→data-table: %w", c.ctype, err)
			}
		}
	}
	return nil
}
