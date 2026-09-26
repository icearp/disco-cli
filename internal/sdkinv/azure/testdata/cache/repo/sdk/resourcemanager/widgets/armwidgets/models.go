package armwidgets

// SubResource - Reference to another resource.
type SubResource struct {
	// Resource Id
	ID *string
}

// Widget - A widget.
type Widget struct {
	// REQUIRED; Resource location.
	Location *string

	// Resource properties.
	Properties *WidgetProperties

	// Resource tags.
	Tags map[string]*string

	// READ-ONLY; Resource ID.
	ID *string

	// READ-ONLY; Resource name.
	Name *string

	// READ-ONLY; Resource type.
	Type *string
}

// WidgetExtra - Nested settings.
type WidgetExtra struct {
	// The policy applied.
	PolicyID *string

	// Plain setting.
	Mode *string
}

// WidgetListResult - The list operation response.
type WidgetListResult struct {
	// REQUIRED; The list of widgets.
	Value []*Widget

	// The URI to fetch the next page.
	NextLink *string
}

// WidgetProperties - Properties of a widget.
type WidgetProperties struct {
	// Nested settings.
	Extra *WidgetExtra

	// The provisioning state.
	ProvisioningState *string

	// Subnet the widget sits in.
	SubnetID *string

	// Vault the widget stores secrets in.
	Vault *SubResource
}
