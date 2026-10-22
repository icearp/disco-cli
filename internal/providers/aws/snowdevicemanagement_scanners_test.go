package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/snowdevicemanagement"
	snowdevicemanagementtypes "github.com/aws/aws-sdk-go-v2/service/snowdevicemanagement/types"
)

// TestScanSnowDeviceManagement_ReadsTags pins that managed-device and task
// rows carry their listed element's Tags, and that an untagged element stores
// none.
func TestScanSnowDeviceManagement_ReadsTags(t *testing.T) {
	const (
		devTagged    = "arn:aws:snow-device-management:us-east-1:123456789012:managed-device/smd-1"
		devUntagged  = "arn:aws:snow-device-management:us-east-1:123456789012:managed-device/smd-2"
		taskTagged   = "arn:aws:snow-device-management:us-east-1:123456789012:task/t-1"
		taskUntagged = "arn:aws:snow-device-management:us-east-1:123456789012:task/t-2"
	)
	handlers := map[string]func(in any) (any, error){
		"ListDevices": func(any) (any, error) {
			return &snowdevicemanagement.ListDevicesOutput{Devices: []snowdevicemanagementtypes.DeviceSummary{
				{ManagedDeviceArn: ptrStr(devTagged), ManagedDeviceId: ptrStr("smd-1"), Tags: map[string]string{"site": "a"}},
				{ManagedDeviceArn: ptrStr(devUntagged), ManagedDeviceId: ptrStr("smd-2")},
			}}, nil
		},
		"ListTasks": func(any) (any, error) {
			return &snowdevicemanagement.ListTasksOutput{Tasks: []snowdevicemanagementtypes.TaskSummary{
				{TaskArn: ptrStr(taskTagged), TaskId: ptrStr("t-1"), Tags: map[string]string{"owner": "ops"}},
				{TaskArn: ptrStr(taskUntagged), TaskId: ptrStr("t-2")},
			}}, nil
		},
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, _, err := scanSnowDeviceManagement(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanSnowDeviceManagement: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}

	devices := rowsByNativeID(t, st, TypeSnowDeviceManagementManagedDevice)
	assertTagsMap(t, devices[devTagged].TagsJSON, map[string]string{"site": "a"})
	if got := devices[devUntagged].TagsJSON; got != nil {
		t.Errorf("untagged device TagsJSON = %q, want nil", *got)
	}
	tasks := rowsByNativeID(t, st, TypeSnowDeviceManagementTask)
	assertTagsMap(t, tasks[taskTagged].TagsJSON, map[string]string{"owner": "ops"})
	if got := tasks[taskUntagged].TagsJSON; got != nil {
		t.Errorf("untagged task TagsJSON = %q, want nil", *got)
	}
}
