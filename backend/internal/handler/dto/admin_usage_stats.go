package dto

import "github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"

type AdminUsageStats struct {
	usagestats.UsageStats
	InputActualCost         float64 `json:"input_actual_cost"`
	OutputActualCost        float64 `json:"output_actual_cost"`
	CacheCreationActualCost float64 `json:"cache_creation_actual_cost"`
	CacheReadActualCost     float64 `json:"cache_read_actual_cost"`
	OtherActualCost         float64 `json:"other_actual_cost"`
}

func AdminUsageStatsFromUsageStats(stats *usagestats.UsageStats) *AdminUsageStats {
	if stats == nil {
		return nil
	}
	return &AdminUsageStats{
		UsageStats:              *stats,
		InputActualCost:         stats.InputActualCost,
		OutputActualCost:        stats.OutputActualCost,
		CacheCreationActualCost: stats.CacheCreationActualCost,
		CacheReadActualCost:     stats.CacheReadActualCost,
		OtherActualCost:         stats.OtherActualCost,
	}
}

type AdminModelStat struct {
	usagestats.ModelStat
	InputActualCost         float64 `json:"input_actual_cost"`
	OutputActualCost        float64 `json:"output_actual_cost"`
	CacheCreationActualCost float64 `json:"cache_creation_actual_cost"`
	CacheReadActualCost     float64 `json:"cache_read_actual_cost"`
	OtherActualCost         float64 `json:"other_actual_cost"`
}

func AdminModelStatsFromUsageStats(stats []usagestats.ModelStat) []AdminModelStat {
	if stats == nil {
		return nil
	}
	result := make([]AdminModelStat, 0, len(stats))
	for i := range stats {
		result = append(result, AdminModelStat{
			ModelStat:               stats[i],
			InputActualCost:         stats[i].InputActualCost,
			OutputActualCost:        stats[i].OutputActualCost,
			CacheCreationActualCost: stats[i].CacheCreationActualCost,
			CacheReadActualCost:     stats[i].CacheReadActualCost,
			OtherActualCost:         stats[i].OtherActualCost,
		})
	}
	return result
}
