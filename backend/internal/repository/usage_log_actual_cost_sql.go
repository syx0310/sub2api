package repository

import "fmt"

type actualCostBreakdownSQL struct {
	Input         string
	Output        string
	CacheCreation string
	CacheRead     string
	Other         string
}

// buildActualCostBreakdownSQL returns per-row weighted aggregates. actualCostExpr
// must be a row-level expression matching the endpoint's existing actual-cost
// semantics, not an aggregate expression.
func buildActualCostBreakdownSQL(alias, actualCostExpr string) actualCostBreakdownSQL {
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	nonNegative := func(expr string) string {
		return fmt.Sprintf("GREATEST(COALESCE(%s, 0), 0)", expr)
	}

	totalCost := nonNegative(column("total_cost"))
	actualCost := fmt.Sprintf("COALESCE(%s, 0)", actualCostExpr)

	inputCost := nonNegative(column("input_cost"))
	outputCost := nonNegative(column("output_cost"))
	cacheCreationCost := nonNegative(column("cache_creation_cost"))
	cacheReadCost := nonNegative(column("cache_read_cost"))
	knownCost := fmt.Sprintf("(%s + %s + %s + %s)", inputCost, outputCost, cacheCreationCost, cacheReadCost)
	// Historical rows can contain component totals greater than total_cost. Use
	// the larger basis so known components never allocate more than actual_cost.
	allocationBase := fmt.Sprintf("GREATEST(%s, %s)", totalCost, knownCost)
	scale := fmt.Sprintf(
		"CASE WHEN %s > 0 AND %s > 0 THEN %s / %s ELSE 0 END",
		actualCost,
		allocationBase,
		actualCost,
		allocationBase,
	)

	weightedSum := func(costExpr string) string {
		return fmt.Sprintf("COALESCE(SUM(%s * (%s)), 0)", costExpr, scale)
	}

	return actualCostBreakdownSQL{
		Input:         weightedSum(inputCost),
		Output:        weightedSum(outputCost),
		CacheCreation: weightedSum(cacheCreationCost),
		CacheRead:     weightedSum(cacheReadCost),
		Other: fmt.Sprintf(
			"COALESCE(SUM(%s - %s * (%s)), 0)",
			actualCost,
			knownCost,
			scale,
		),
	}
}
