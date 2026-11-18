package core

// Presenter is an optional feature capability: it renders the successful
// result for humans (CLI and TUI). Width 0 means no wrapping. UI assemblies
// fall back to a generic message when a feature has no presenter.
type Presenter interface {
	Present(res Result, width int) string
}
