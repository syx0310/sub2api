package repository

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ensureRelayModeBackendMode auto-enables backend_mode when running in relay mode.
// Backend mode disables user registration and self-service, which is required for relay mode
// where all API keys belong to the admin user.
// If backend_mode_enabled is already "true", this is a no-op.
func ensureRelayModeBackendMode(ctx context.Context, client *dbent.Client) error {
	if client == nil {
		return fmt.Errorf("nil ent client")
	}

	// Check current value
	existing, err := client.Setting.Query().
		Where(setting.KeyEQ(service.SettingKeyBackendModeEnabled)).
		Only(ctx)
	if err == nil && existing.Value == "true" {
		return nil
	}

	// Auto-enable backend mode
	slog.Warn("relay mode: backend_mode_enabled was not set, auto-enabling to block user self-service")

	now := time.Now()
	return client.Setting.Create().
		SetKey(service.SettingKeyBackendModeEnabled).
		SetValue("true").
		SetUpdatedAt(now).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
}
