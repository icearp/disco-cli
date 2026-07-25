package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/virtualmachineimagebuilder/armvirtualmachineimagebuilder"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeVMImageBuilderImageTemplate, Service: "microsoft.virtualmachineimages"})
	registerService(serviceEntry{
		name: "azure:microsoft.virtualmachineimages",
		fn:   scanVMImageBuilder,
	})
}

// scanVMImageBuilder discovers VM Image Builder image templates
// (Microsoft.VirtualMachineImages/imageTemplates).
func scanVMImageBuilder(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armvirtualmachineimagebuilder.NewVirtualMachineImageTemplatesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armvirtualmachineimagebuilder:NewVirtualMachineImageTemplatesClient: %w", err)
	}
	return azSimpleScan(ctx, "armvirtualmachineimagebuilder:VirtualMachineImageTemplates.List", TypeVMImageBuilderImageTemplate, sub, st, scanID,
		client.NewListPager(nil),
		func(p armvirtualmachineimagebuilder.VirtualMachineImageTemplatesClientListResponse) []*armvirtualmachineimagebuilder.ImageTemplate {
			return p.Value
		},
		func(r *armvirtualmachineimagebuilder.ImageTemplate) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
