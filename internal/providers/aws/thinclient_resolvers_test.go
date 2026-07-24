package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	thinclienttypes "github.com/aws/aws-sdk-go-v2/service/workspacesthinclient/types"
)

func TestResolveThinClientDeviceEnvironment(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	envID := "env-0001"
	envARN := fmt.Sprintf("arn:aws:thinclient:%s:%s:environment/%s", testRegion, acct.ID, envID)
	devARN := fmt.Sprintf("arn:aws:thinclient:%s:%s:device/dev-0001", testRegion, acct.ID)

	eAttrs := mustJSON(thinclienttypes.EnvironmentSummary{Id: aws.String(envID), Arn: aws.String(envARN)})
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeThinClientEnvironment, envARN, testRegion, eAttrs)
	dAttrs := mustJSON(thinclienttypes.DeviceSummary{Arn: aws.String(devARN), EnvironmentId: aws.String(envID)})
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeThinClientDevice, devARN, testRegion, dAttrs)

	if err := resolveThinClientDeviceEnvironment(acct, st); err != nil {
		t.Fatalf("resolveThinClientDeviceEnvironment: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, eID, store.RelAttachedTo)
}

func TestResolveThinClientDeviceEnvironment_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	devARN := fmt.Sprintf("arn:aws:thinclient:%s:%s:device/dev-x", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeThinClientDevice, devARN, testRegion, "{}")

	if err := resolveThinClientDeviceEnvironment(acct, st); err != nil {
		t.Fatalf("resolveThinClientDeviceEnvironment: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	if len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}
