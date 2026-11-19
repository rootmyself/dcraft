// Package selector resolves the explicit Compose service and container
// instance a live operation (logs, exec) targets. It reads the common
// read-only stack snapshot, never contacts Docker itself, and is published so
// a later lifecycle screen can reuse the same service/instance vocabulary
// instead of inventing another selection model.
package selector

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Instance is one observed container of a Compose service. Index is the
// zero-based position in the service's stable instance list; it is exactly the
// value of the CLI's -i/--index flag (the Compose adapter maps it to Compose's
// one-based --index).
type Instance struct {
	Index   int
	Name    string
	State   string
	Running bool
}

// Service is a configured Compose service and the instances observed for it.
type Service struct {
	Name      string
	Instances []Instance
}

// Selection is one concrete service, optionally narrowed to one instance.
// Chosen is nil when the caller did not request an instance and the whole
// service is the target (logs show every instance).
type Selection struct {
	Service   Service
	Instances []Instance
	Chosen    *Instance
}

// Services returns the services of the current Compose configuration in the
// snapshot's stable order. Instances observed for services that are not part
// of the active configuration are ignored: they are not selectable targets.
func Services(snap stack.Snapshot) []Service {
	out := make([]Service, 0, len(snap.Services))
	for _, svc := range snap.Services {
		if !svc.Configured {
			continue
		}
		out = append(out, Service{Name: svc.Name, Instances: instancesOf(svc)})
	}
	return out
}

func instancesOf(svc stack.Service) []Instance {
	out := make([]Instance, 0, len(svc.Instances))
	for i, in := range svc.Instances {
		out = append(out, Instance{
			Index:   i,
			Name:    in.Name,
			State:   in.State,
			Running: in.State == string(stack.StateRunning),
		})
	}
	return out
}

// Select validates explicit service names and returns them in the requested
// order. An empty request means every configured service (logs default).
func Select(snap stack.Snapshot, requested []string) ([]Service, error) {
	available := Services(snap)
	byName := make(map[string]Service, len(available))
	for _, svc := range available {
		byName[svc.Name] = svc
	}
	if len(requested) == 0 {
		return available, nil
	}
	out := make([]Service, 0, len(requested))
	seen := map[string]bool{}
	var unknown []string
	for _, raw := range requested {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		svc, ok := byName[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		out = append(out, svc)
	}
	if len(unknown) > 0 {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryServiceUnknown,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgUnknownService, strings.Join(unknown, ", ")),
			Detail:      msgAvailableServices + serviceNames(available),
			NextActions: []string{msgUnknownServiceActionOne, msgUnknownServiceActionTwo},
		})
	}
	return out, nil
}

// ChooseInstance narrows one selected service to a zero-based instance index.
// A negative index keeps the whole service. A stopped instance remains
// loggable: its previous output is still user data worth reading.
func ChooseInstance(svc Service, index int) (Selection, error) {
	sel := Selection{Service: svc, Instances: svc.Instances}
	if index < 0 {
		return sel, nil
	}
	if index >= len(svc.Instances) {
		return Selection{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInstanceUnknown,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgInstanceOutOfRange, index),
			Detail:      instanceList(svc),
			NextActions: []string{msgInstanceActionOne, msgInstanceActionTwo},
		})
	}
	chosen := svc.Instances[index]
	sel.Chosen = &chosen
	return sel, nil
}

// OneRunning resolves exactly one service and one running instance for exec.
// It never picks the first instance of a scaled service: without an explicit
// index the caller receives the instance list and a usage failure (exit 2)
// instead of an implicit choice.
func OneRunning(snap stack.Snapshot, requested []string, index int) (Selection, error) {
	available := Services(snap)
	services, err := Select(snap, requested)
	if err != nil {
		return Selection{}, err
	}
	switch {
	case len(requested) == 0:
		return Selection{}, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     msgServiceRequired,
			Detail:      msgAvailableServices + serviceNames(available),
			NextActions: []string{msgServiceRequiredActionOne, msgServiceRequiredActionTwo},
		})
	case len(services) > 1:
		return Selection{}, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgMultipleServices, serviceNames(services)),
			Detail:      msgAvailableServices + serviceNames(available),
			NextActions: []string{msgServiceRequiredActionOne, msgServiceRequiredActionTwo},
		})
	}

	svc := services[0]
	if index >= 0 {
		sel, err := ChooseInstance(svc, index)
		if err != nil {
			return Selection{}, err
		}
		if !sel.Chosen.Running {
			return Selection{}, core.Fail(core.Diagnostic{
				Category:    core.CategoryInstanceStopped,
				Severity:    core.SeverityError,
				Message:     fmt.Sprintf(msgChosenInstanceStopped, sel.Chosen.Name, sel.Chosen.State),
				Detail:      instanceList(svc),
				NextActions: []string{msgStoppedActionOne, msgStoppedActionTwo},
			})
		}
		return sel, nil
	}

	var running []Instance
	for _, in := range svc.Instances {
		if in.Running {
			running = append(running, in)
		}
	}
	switch {
	case len(running) == 0:
		return Selection{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInstanceStopped,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgServiceNotRunning, svc.Name),
			Detail:      instanceList(svc),
			NextActions: []string{msgStoppedActionOne, msgStoppedActionTwo},
		})
	case len(running) > 1:
		return Selection{}, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgInstanceAmbiguous, svc.Name),
			Detail:      instanceList(svc),
			NextActions: []string{msgExitAmbiguousActionOne, msgInstanceActionTwo},
		})
	default:
		chosen := running[0]
		return Selection{Service: svc, Instances: svc.Instances, Chosen: &chosen}, nil
	}
}

// LogTargets resolves the service list for logs. When an instance index is
// requested, exactly one service must be named explicitly: silently applying
// an index to every service or to a single implicit service would hide which
// container the user is reading.
func LogTargets(snap stack.Snapshot, requested []string, index int) (LogTargetsResult, error) {
	services, err := Select(snap, requested)
	if err != nil {
		return LogTargetsResult{}, err
	}
	out := LogTargetsResult{Services: services}
	if index < 0 {
		return out, nil
	}
	if len(requested) == 0 || len(services) != 1 {
		return LogTargetsResult{}, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     msgIndexNeedsOneService,
			Detail:      msgAvailableServices + serviceNames(services),
			NextActions: []string{msgIndexNeedsOneServiceActionOne, msgIndexNeedsOneServiceActionTwo},
		})
	}
	sel, err := ChooseInstance(services[0], index)
	if err != nil {
		return LogTargetsResult{}, err
	}
	out.Picked = &sel
	return out, nil
}

// LogTargetsResult is the logs selection: every selected service plus the one
// narrowed instance when an index was requested.
type LogTargetsResult struct {
	Services []Service
	Picked   *Selection
}

func serviceNames(services []Service) string {
	names := make([]string, 0, len(services))
	for _, svc := range services {
		names = append(names, svc.Name)
	}
	return strings.Join(names, ", ")
}

// instanceList is the human list used in diagnostics and guidance. The index
// is zero-based because that is what -i/--index accepts.
func instanceList(svc Service) string {
	if len(svc.Instances) == 0 {
		return fmt.Sprintf(msgNoInstances, svc.Name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, msgInstanceListHeader, svc.Name)
	for _, in := range svc.Instances {
		fmt.Fprintf(&b, "\n#%d %s (%s)", in.Index, in.Name, in.State)
	}
	return b.String()
}
