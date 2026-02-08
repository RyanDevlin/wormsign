// Package cel implements a CEL-based custom detector engine for evaluating
// WormsignDetector CRD expressions against Kubernetes resource objects.
// See Section 4.2 of the project spec and DECISIONS.md D4.
package cel

import (
	"fmt"

	"github.com/google/cel-go/cel"
)

// DefaultCostLimit is the maximum CEL evaluation cost budget per expression.
const DefaultCostLimit = 1000

// Evaluator compiles and evaluates CEL expressions for custom detectors.
type Evaluator struct {
	costLimit uint64
}

// NewEvaluator creates a new CEL expression evaluator with the given cost limit.
// If costLimit is 0, DefaultCostLimit is used.
func NewEvaluator(costLimit uint64) *Evaluator {
	if costLimit == 0 {
		costLimit = DefaultCostLimit
	}
	return &Evaluator{costLimit: costLimit}
}

// Compile validates and compiles a CEL expression string. It returns an error
// if the expression is syntactically or semantically invalid.
func (e *Evaluator) Compile(expression string) (*cel.Ast, error) {
	env, err := cel.NewEnv()
	if err != nil {
		return nil, fmt.Errorf("creating CEL environment: %w", err)
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compiling CEL expression: %w", issues.Err())
	}
	return ast, nil
}
