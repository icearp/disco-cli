package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveSchedulerScheduleTargets,
		EdgeDecl{TypeSchedulerSchedule, TypeSchedulerScheduleGroup, store.RelAttachedTo},
		EdgeDecl{TypeSchedulerSchedule, TypeLambdaFunction, store.RelRoutesTo},
		EdgeDecl{TypeSchedulerSchedule, TypeSNSTopic, store.RelRoutesTo},
		EdgeDecl{TypeSchedulerSchedule, TypeSQSQueue, store.RelRoutesTo},
		EdgeDecl{TypeSchedulerSchedule, TypeKinesisStream, store.RelRoutesTo},
		EdgeDecl{TypeSchedulerSchedule, TypeSFNStateMachine, store.RelRoutesTo},
		EdgeDecl{TypeSchedulerSchedule, TypeFirehoseDeliveryStream, store.RelRoutesTo},
		EdgeDecl{TypeSchedulerSchedule, TypeEventsAPIDestination, store.RelRoutesTo},
	)
}

// resolveSchedulerScheduleTargets emits one edge per schedule:
//   - schedule → schedule-group (attached-to) via GroupName
//   - schedule → target (routes-to) via Target.Arn substring dispatch
//
// Reuses eventBridgeTargetType — Scheduler targets the same AWS service
// catalogue as EventBridge rules.
func resolveSchedulerScheduleTargets(acct *account, st *store.Store) error {
	schedules, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSchedulerSchedule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(schedules) == 0 {
		return nil
	}
	groupSet, err := scannedIDSet(acct, st, TypeSchedulerScheduleGroup)
	if err != nil {
		return err
	}

	type attrs struct {
		GroupName *string `json:"GroupName"`
		Target    *struct {
			Arn *string `json:"Arn"`
		} `json:"Target"`
	}
	for _, s := range schedules {
		var a attrs
		if err := json.Unmarshal([]byte(s.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(s.Region)
		if name := sv(a.GroupName); name != "" {
			groupARN := fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule-group/%s", region, acct.ID, name)
			groupID := store.ResourceID("aws", acct.ID, groupARN)
			if groupSet[groupID] {
				if err := st.UpsertRelationship(s.ID, groupID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert scheduler-schedule→group: %w", err)
				}
			}
		}
		if a.Target == nil {
			continue
		}
		arn := sv(a.Target.Arn)
		if arn == "" {
			continue
		}
		targetType := eventBridgeTargetType(arn)
		if targetType == "" {
			continue
		}
		targetID := store.ResourceID("aws", acct.ID, arn)
		if err := st.UpsertRelationship(s.ID, targetID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert scheduler-schedule→target: %w", err)
		}
	}
	return nil
}
