//go:build unit

package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type performanceWindowStart struct{ before time.Time }

func (m performanceWindowStart) Match(value driver.Value) bool {
	start, ok := value.(time.Time)
	return ok && !start.Before(m.before.Add(-5*time.Minute)) && !start.After(time.Now().Add(-5*time.Minute))
}

func TestPerformanceStatsCostSharesRateQuery(t *testing.T) {
	for _, scope := range []string{"global", "user", "api_key"} {
		for _, outcome := range []string{"fractional", "empty", "error"} {
			t.Run(scope+"/"+outcome, func(t *testing.T) {
				db, mock := newSQLMock(t)
				repo := &usageLogRepository{sql: db}
				tokens := "input_tokens + output_tokens"
				filter := ""
				args := []driver.Value{performanceWindowStart{before: time.Now()}}
				if scope == "user" {
					filter = " AND user_id = $2"
					args = append(args, int64(42))
				} else if scope == "api_key" {
					tokens += " + cache_creation_tokens + cache_read_tokens"
					filter = " AND api_key_id = $2"
					args = append(args, int64(42))
				}
				query := "SELECT COUNT(*) FILTER (WHERE NOT prewarm) as request_count, " +
					"COALESCE(SUM(" + tokens + "), 0) as token_count, " +
					"COALESCE(SUM(actual_cost), 0) as actual_cost FROM usage_logs WHERE created_at >= $1" + filter
				expectation := mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(args...)
				queryErr := errors.New("performance query failed")
				switch outcome {
				case "fractional":
					expectation.WillReturnRows(sqlmock.NewRows([]string{"request_count", "token_count", "actual_cost"}).AddRow(12, 101, 1.234567))
				case "empty":
					expectation.WillReturnRows(sqlmock.NewRows([]string{"request_count", "token_count", "actual_cost"}).AddRow(0, 0, 0))
				case "error":
					expectation.WillReturnError(queryErr)
				}

				var rpm, tpm int64
				var cost float64
				var err error
				if scope == "api_key" {
					rpm, tpm, cost, err = repo.getPerformanceStatsByAPIKey(context.Background(), 42)
				} else {
					var userID int64
					if scope == "user" {
						userID = 42
					}
					rpm, tpm, cost, err = repo.getPerformanceStats(context.Background(), userID)
				}
				if outcome == "error" {
					require.ErrorIs(t, err, queryErr)
				} else {
					require.NoError(t, err)
				}
				if outcome == "fractional" {
					require.Equal(t, int64(2), rpm)
					require.Equal(t, int64(20), tpm)
					require.InDelta(t, 0.2469134, cost, 1e-12)
				} else {
					require.Zero(t, rpm)
					require.Zero(t, tpm)
					require.Zero(t, cost)
				}
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
