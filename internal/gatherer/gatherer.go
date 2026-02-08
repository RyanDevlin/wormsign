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

// RunAll executes all provided gatherers concurrently for the given resource
// and returns the collected diagnostic sections. Individual gatherer failures
// are recorded in the section's Error field without canceling other gatherers
// (see DECISIONS.md D12).
func RunAll(ctx context.Context, logger *slog.Logger, gatherers []Gatherer, ref model.ResourceRef) []model.DiagnosticSection {
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
