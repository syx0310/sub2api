package repository

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildActualCostBreakdownSQLUsesPerRowScale(t *testing.T) {
	expressions := buildActualCostBreakdownSQL("ul", "ul.actual_cost")
	all := strings.Join([]string{
		expressions.Input,
		expressions.Output,
		expressions.CacheCreation,
		expressions.CacheRead,
		expressions.Other,
	}, " ")

	require.Contains(t, all, "CASE WHEN COALESCE(ul.actual_cost, 0) > 0")
	require.Contains(t, all, "GREATEST(GREATEST(COALESCE(ul.total_cost, 0), 0)")
	require.Contains(t, all, "COALESCE(ul.actual_cost, 0) / GREATEST(")
	require.Contains(t, expressions.Other, "COALESCE(SUM(COALESCE(ul.actual_cost, 0) -")
	require.NotContains(t, all, "SUM(actual_cost) / SUM(total_cost)")
	require.NotContains(t, strings.ToUpper(all), "ROUND(")
}

func TestBuildActualCostBreakdownSQLPreservesSignedActualCostInOther(t *testing.T) {
	expressions := buildActualCostBreakdownSQL("", "actual_cost")

	require.NotContains(t, expressions.Other, "GREATEST(COALESCE(actual_cost, 0), 0)")
	require.Contains(t, expressions.Other, "COALESCE(actual_cost, 0) -")
	require.Contains(t, expressions.Input, "CASE WHEN COALESCE(actual_cost, 0) > 0")
}

func TestBuildActualCostBreakdownSQLSupportsAccountActualCostBasis(t *testing.T) {
	basis := "COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)"
	expressions := buildActualCostBreakdownSQL("", basis)

	require.Contains(t, expressions.Input, basis)
	require.Contains(t, expressions.Other, basis)
}
