// Package present owns the one mapping from a stack state to the label and
// badge the human interfaces draw. Features and the TUI consume it instead of
// keeping private copies, so the status table, the lifecycle table and the
// service scope table cannot drift apart.
//
// It sits beside the stack types rather than in core: core defines the badge
// kinds but must not import stack (stack imports core), and it sits below the
// features rather than in a feature so every surface can share it.
package present

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// StateLabel is the Korean label of one aggregate service state. An unknown
// machine value is shown as-is: inventing a Korean word for a state this build
// does not know would hide the fact.
func StateLabel(state stack.ServiceState) string {
	switch state {
	case stack.StateRunning:
		return msgStateRunning
	case stack.StateExited:
		return msgStateExited
	case stack.StatePaused:
		return msgStatePaused
	case stack.StateRestarting:
		return msgStateRestarting
	case stack.StateCreated:
		return msgStateCreated
	case stack.StateNotCreated:
		return msgStateNotCreated
	case "":
		return msgStateUnknown
	default:
		return string(state)
	}
}

// StateLabelValue is StateLabel for the raw machine value published by
// core.ServiceStatus, which mirrors stack.ServiceState as a string.
func StateLabelValue(state string) string { return StateLabel(stack.ServiceState(state)) }

// StateBadge pairs a state with its semantic icon. Paused deliberately has no
// badge: `■ 일시정지됨` is one cell wider than the approved state column, and
// the shorter fallback `■ 중지` would name the wrong state. The Korean label
// alone carries the meaning and still fits.
func StateBadge(state stack.ServiceState) core.BadgeKind {
	switch state {
	case stack.StateRunning:
		return core.BadgeRunning
	case stack.StateExited:
		return core.BadgeStopped
	case stack.StateRestarting:
		return core.BadgeStarting
	case stack.StatePaused:
		return core.BadgeNone
	default:
		return core.BadgeNotCreated
	}
}

// StateBadgeValue is StateBadge for the raw value of core.ServiceStatus.
func StateBadgeValue(state string) core.BadgeKind { return StateBadge(stack.ServiceState(state)) }

// HealthLabel is the TUI wording of one healthcheck result. Its sentences are
// deliberately short: the column is eight cells, and the icon carries the same
// meaning as a second channel.
func HealthLabel(health stack.Health) string {
	switch health {
	case stack.HealthNone, "":
		return msgHealthNone
	case stack.HealthHealthy:
		return msgHealthHealthy
	case stack.HealthStarting:
		return msgHealthStarting
	case stack.HealthUnhealthy:
		return msgHealthUnhealthy
	default:
		return string(health)
	}
}

// HealthLabelValue is HealthLabel for the raw value of core.ServiceStatus.
func HealthLabelValue(health string) string { return HealthLabel(stack.Health(health)) }

// HealthBadge pairs a healthcheck result with its semantic icon. None carries
// no badge: a missing healthcheck is not a state of the container.
func HealthBadge(health stack.Health) core.BadgeKind {
	switch health {
	case stack.HealthUnhealthy:
		return core.BadgeUnhealthy
	case stack.HealthStarting:
		return core.BadgeStarting
	default:
		return core.BadgeNone
	}
}

// HealthBadgeValue is HealthBadge for the raw value of core.ServiceStatus.
func HealthBadgeValue(health string) core.BadgeKind { return HealthBadge(stack.Health(health)) }

// ReadinessBadge pairs an update readiness classification with its icon. It
// lives here so every state-to-badge rule has one home.
func ReadinessBadge(status stack.Readiness) core.BadgeKind {
	switch status {
	case stack.ReadinessHealthy, stack.ReadinessRunningUnverified:
		return core.BadgeRunning
	case stack.ReadinessUnhealthy:
		return core.BadgeUnhealthy
	case stack.ReadinessRestarting, stack.ReadinessTimeout:
		return core.BadgeStarting
	case stack.ReadinessExited, stack.ReadinessNotStarted, stack.ReadinessNotCreated:
		return core.BadgeStopped
	default:
		return core.BadgeNone
	}
}
