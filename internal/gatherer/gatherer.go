// Package gatherer defines the Gatherer interface and orchestrates concurrent
// diagnostic data collection for fault events. See Section 5.2 of the project
// spec and DECISIONS.md D12.
package gatherer

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// Gatherer collects diagnostic data relevant to a fault event.
type Gatherer interface {
	// Name returns the gatherer identifier (e.g., "PodEvents", "PodLogs").
	Name() string

	// Gather collects diagnostic data for the given resource reference.
	Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error)
}

// TriggerMatcher is an optional interface that gatherers can implement to
// indicate which fault events they should respond to. If a gatherer does not
// implement TriggerMatcher, it is invoked for all fault events.
type TriggerMatcher interface {
	// ResourceKinds returns the Kubernetes resource kinds this gatherer
	// responds to (e.g., "Pod", "Node"). An empty slice means all kinds.
	ResourceKinds() []string

	// DetectorNames returns the detector names this gatherer responds to
	// (e.g., "PodCrashLoop", "PodFailed"). An empty slice means all detectors.
	DetectorNames() []string
}

// MatchesTrigger reports whether a gatherer should be invoked for the given
// resource kind and detector name. If the gatherer does not implement
// TriggerMatcher, it always matches. Both resource kind and detector name
// conditions must be satisfied (AND logic). An empty list for either condition
// is treated as "match all".
func MatchesTrigger(g Gatherer, resourceKind, detectorName string) bool {
	tm, ok := g.(TriggerMatcher)
	if !ok {
		return true
	}

	kinds := tm.ResourceKinds()
	if len(kinds) > 0 {
		found := false
		for _, k := range kinds {
			if k == resourceKind {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	detectors := tm.DetectorNames()
	if len(detectors) > 0 {
		found := false
		for _, d := range detectors {
			if d == detectorName {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// FilterByTrigger returns the subset of gatherers that match the given resource
// kind and detector name.
func FilterByTrigger(gatherers []Gatherer, resourceKind, detectorName string) []Gatherer {
	var matched []Gatherer
	for _, g := range gatherers {
		if MatchesTrigger(g, resourceKind, detectorName) {
			matched = append(matched, g)
		}
	}
	return matched
}

// RunAll executes all provided gatherers concurrently for the given resource
// and returns the collected diagnostic sections. Individual gatherer failures
// are recorded in the section's Error field without canceling other gatherers
// (see DECISIONS.md D12).
func RunAll(ctx context.Context, logger *slog.Logger, gatherers []Gatherer, ref model.ResourceRef) []model.DiagnosticSection {
	if len(gatherers) == 0 {
		return nil
	}

	sections := make([]model.DiagnosticSection, len(gatherers))

	g, gCtx := errgroup.WithContext(ctx)
	for i, gath := range gatherers {
		i, gath := i, gath
		g.Go(func() error {
			start := time.Now()
			section, err := gath.Gather(gCtx, ref)
			duration := time.Since(start)

			if err != nil {
				logger.Warn("gatherer failed",
					"gatherer", gath.Name(),
					"resource", ref.String(),
					"duration", duration,
					"error", err,
				)
				sections[i] = model.DiagnosticSection{
					GathererName: gath.Name(),
					Title:        gath.Name(),
					Error:        err.Error(),
				}
				// Return nil so other gatherers continue.
				return nil
			}

			sections[i] = *section
			logger.Debug("gatherer completed",
				"gatherer", gath.Name(),
				"resource", ref.String(),
				"duration", duration,
			)
			return nil
		})
	}

	// errgroup.Wait returns nil since we always return nil from goroutines.
	_ = g.Wait()
	return sections
}

// GatherForFaultEvent filters the available gatherers by trigger matching and
// runs the matching ones concurrently for the fault event's resource.
func GatherForFaultEvent(ctx context.Context, logger *slog.Logger, gatherers []Gatherer, event *model.FaultEvent) []model.DiagnosticSection {
	if event == nil {
		return nil
	}
	matched := FilterByTrigger(gatherers, event.Resource.Kind, event.DetectorName)
	return RunAll(ctx, logger, matched, event.Resource)
}

// GatherForSuperEvent gathers diagnostics for a super-event. It gathers for
// the primary resource and up to maxAffectedResources sampled affected
// resources from the constituent fault events.
func GatherForSuperEvent(ctx context.Context, logger *slog.Logger, gatherers []Gatherer, event *model.SuperEvent, maxAffectedResources int) []model.DiagnosticSection {
	if event == nil {
		return nil
	}
	if maxAffectedResources < 0 {
		maxAffectedResources = 0
	}

	// Gather for the primary resource using the first fault event's detector
	// name for trigger matching (or empty if no fault events).
	detectorName := ""
	if len(event.FaultEvents) > 0 {
		detectorName = event.FaultEvents[0].DetectorName
	}

	matched := FilterByTrigger(gatherers, event.PrimaryResource.Kind, detectorName)
	sections := RunAll(ctx, logger, matched, event.PrimaryResource)

	// Gather for a sample of affected resources. We select unique resources
	// that differ from the primary resource, up to maxAffectedResources.
	seen := make(map[string]bool)
	primaryKey := event.PrimaryResource.UID
	if primaryKey == "" {
		primaryKey = event.PrimaryResource.Kind + "/" + event.PrimaryResource.Namespace + "/" + event.PrimaryResource.Name
	}
	seen[primaryKey] = true

	sampled := 0
	for _, fe := range event.FaultEvents {
		if sampled >= maxAffectedResources {
			break
		}

		key := fe.Resource.UID
		if key == "" {
			key = fe.Resource.Kind + "/" + fe.Resource.Namespace + "/" + fe.Resource.Name
		}
		if seen[key] {
			continue
		}
		seen[key] = true

		affectedMatched := FilterByTrigger(gatherers, fe.Resource.Kind, fe.DetectorName)
		affectedSections := RunAll(ctx, logger, affectedMatched, fe.Resource)
		sections = append(sections, affectedSections...)
		sampled++
	}

	return sections
}
