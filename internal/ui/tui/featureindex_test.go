package tui

// featureIndex is a test helper: the navigation index of a feature's first menu
// entry, or -1. The model itself addresses menu entries, not features, so tests
// that name a feature ask the resolved list which row that is.
func (m *Model) featureIndex(name string) int {
	return m.menuIndexForFeature(name)
}
