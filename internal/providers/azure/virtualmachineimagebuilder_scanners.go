package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/virtualmachineimagebuilder/armvirtualmachineimagebuilder"
)

func init() {
	registerService(serviceEntry{
		name: "azure:virtualmachineimagebuilder",
		fn:   scanVMImageBuilder,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.virtualmachineimages", DiscoType: TypeVMImageBuilderImageTemplate},
		},
	})
}

// scanVMImageBuilder discovers VM Image Builder image templates
// (Microsoft.VirtualMachineImages/imageTemplates).
func scanVMImageBuilder(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
