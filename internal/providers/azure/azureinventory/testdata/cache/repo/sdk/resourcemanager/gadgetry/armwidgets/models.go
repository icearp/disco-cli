package armwidgets

type Widget struct {
	Location *string
	Tags     map[string]*string
	ID       *string
	Name     *string
	Type     *string
}

type WidgetListResult struct {
	Value    []*Widget
	NextLink *string
}
