package securityaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

type activeConfigSnapshot struct {
	storage  storageConfig
	active   ActiveConfig
	loadedAt time.Time
}

type ConfigManager struct {
	db        *sql.DB
	settings  service.SettingRepository
	redis     *redis.Client
	encryptor SecretEncryptor
	clock     Clock
	// Persistent encrypted state must not use the process-local fallback key.
	// With an auto-generated key, newly saved endpoint tokens would become
	// undecryptable after the next restart (issue #4887).
	persistentKeyConfigured bool

	snapshot atomic.Pointer[activeConfigSnapshot]
	expected atomic.Int64
	// expectedBlocking records the last storage intent that could be decoded,
	// independently of whether endpoint credentials or the full config could be
	// activated. A config version alone cannot distinguish async from blocking.
	expectedBlocking atomic.Bool
	// configUntrusted is set when a load/reload fails before a trustworthy
	// snapshot is installed. Combined with expectedBlocking, EffectiveMode
	// fails closed so a persisted blocking policy cannot be silently skipped
	// after startup or invalidation errors. Without blocking intent, untrusted
	// alone must not force ModeBlocking—Prompt Audit is default-off and must
	// not take the gateway down for every API request (see issue #4560).
	configUntrusted atomic.Bool

	// applyMu serializes local reload/save application. Snapshot installation
	// still performs a version check because a read may have started before a
	// newer snapshot was installed by another goroutine.
	applyMu    sync.Mutex
	expectedMu sync.RWMutex

	stateMu       sync.RWMutex
	lastLoadError string
	lastErrorAt   *time.Time

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewConfigManager(db *sql.DB, settings service.SettingRepository, redisClient *redis.Client, encryptor service.SecretEncryptor, cfg *config.Config) *ConfigManager {
	persistentKeyConfigured := encryptor != nil
	if cfg != nil {
		persistentKeyConfigured = cfg.Totp.EncryptionKeyConfigured
	}
	if keyStatus, ok := encryptor.(interface{ PersistentKeyConfigured() bool }); ok {
		persistentKeyConfigured = keyStatus.PersistentKeyConfigured()
	}
	return &ConfigManager{
		db: db, settings: settings, redis: redisClient, encryptor: encryptor, clock: realClock{},
		persistentKeyConfigured: persistentKeyConfigured,
	}
}

func (m *ConfigManager) Start(ctx context.Context) error {
	if m == nil {
		return errors.New("prompt audit config manager unavailable")
	}
	m.lifecycleMu.Lock()
	if m.cancel != nil {
		m.lifecycleMu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.lifecycleMu.Unlock()
	loadErr := m.Reload(runCtx)
	if loadErr != nil {
		m.markConfigUntrusted()
	}
	m.wg.Add(1)
	go m.refreshLoop(runCtx)
	if m.redis != nil {
		m.wg.Add(1)
		go m.subscribeLoop(runCtx)
	}
	return loadErr
}

func (m *ConfigManager) Shutdown(_ context.Context) error {
	if m == nil {
		return nil
	}
	m.lifecycleMu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
	return nil
}

func (m *ConfigManager) Reload(ctx context.Context) error {
	if m == nil || m.settings == nil {
		m.markUntrustedIfNoActiveSnapshot()
		return errors.New("prompt audit setting repository unavailable")
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	values, err := m.settings.GetMultiple(ctx, []string{SettingKeyPromptAuditConfig, SettingKeyRiskControl})
	if err != nil {
		m.recordLoadError(err)
		m.markUntrustedIfNoActiveSnapshot()
		return err
	}
	m.observeExpectedState(values[SettingKeyPromptAuditConfig], values[SettingKeyRiskControl] == "true")
	storage, err := ParseStorageConfig(values[SettingKeyPromptAuditConfig])
	if err != nil {
		m.recordLoadError(err)
		m.markUntrustedIfNoActiveSnapshot()
		return err
	}
	active, err := ActiveFromStorage(storage, values[SettingKeyRiskControl] == "true", m.encryptor)
	if err != nil {
		m.recordLoadError(err)
		// expectedBlocking may already require fail-closed via BlockingActivationDegraded.
		m.markUntrustedIfNoActiveSnapshot()
		return err
	}
	now := m.clock.Now()
	previous := m.snapshot.Load()
	if !m.installSnapshotIfCurrentOrNewer(&activeConfigSnapshot{
		storage:  cloneStorageConfig(storage),
		active:   cloneActiveConfig(active),
		loadedAt: now,
	}) {
		return nil
	}
	m.configUntrusted.Store(false)
	m.clearLoadError()
	m.logInvalidTokenEndpoints(previous, active)
	LogInfo(EventConfigLoaded, map[string]any{
		"config_version": storage.ConfigVersion, "status": "loaded",
	})
	return nil
}

// logInvalidTokenEndpoints warns once per change (not on every 5s refresh)
// when stored endpoint tokens cannot be decrypted with the current key.
func (m *ConfigManager) logInvalidTokenEndpoints(previous *activeConfigSnapshot, active ActiveConfig) {
	invalid := active.InvalidTokenEndpointIDs()
	if len(invalid) == 0 {
		return
	}
	if previous != nil {
		prior := previous.active.InvalidTokenEndpointIDs()
		if len(prior) == len(invalid) {
			same := true
			for i := range invalid {
				if prior[i] != invalid[i] {
					same = false
					break
				}
			}
			if same && previous.active.ConfigVersion == active.ConfigVersion {
				return
			}
		}
	}
	LogWarn(EventConfigTokenInvalid, map[string]any{
		"config_version": active.ConfigVersion, "status": "degraded",
		"error_code": "endpoint_token_undecryptable", "guard_endpoint_id": strings.Join(invalid, ","),
	})
}

func (m *ConfigManager) Active() (ActiveConfig, bool) {
	if m == nil {
		return ActiveConfig{}, false
	}
	snapshot := m.snapshot.Load()
	if snapshot == nil {
		return ActiveConfig{}, false
	}
	return cloneActiveConfig(snapshot.active), true
}

func (m *ConfigManager) BlockingActivationDegraded() bool {
	if m == nil {
		return false
	}
	// Fail closed only when storage intent requires blocking. Untrusted config
	// without blocking intent must remain ModeOff so administrators can still
	// operate the gateway and turn Prompt Audit off after a failed reload.
	m.expectedMu.RLock()
	expectedBlocking := m.expectedBlocking.Load()
	m.expectedMu.RUnlock()
	if !expectedBlocking {
		return false
	}
	if m.configUntrusted.Load() {
		return true
	}
	active, ok := m.Active()
	if !ok {
		return true
	}
	// A still-active weaker snapshot after a failed blocking activation must not
	// keep serving allow decisions under the old off/async mode.
	return active.EffectiveMode() != ModeBlocking
}

func (m *ConfigManager) EffectiveMode() Mode {
	if m != nil && m.BlockingActivationDegraded() {
		return ModeBlocking
	}
	active, ok := m.Active()
	if !ok {
		return ModeOff
	}
	return active.EffectiveMode()
}

func (m *ConfigManager) markConfigUntrusted() {
	if m == nil {
		return
	}
	m.configUntrusted.Store(true)
}

func (m *ConfigManager) markUntrustedIfNoActiveSnapshot() {
	if m == nil {
		return
	}
	if _, ok := m.Active(); !ok {
		m.markConfigUntrusted()
	}
}

func (m *ConfigManager) Public() (PublicConfig, error) {
	if m == nil {
		return PublicConfig{}, infraerrors.ServiceUnavailable(ErrorCodeConfigUnavailable, "提示词审计配置暂不可用")
	}
	snapshot := m.snapshot.Load()
	if snapshot == nil {
		return PublicConfig{}, infraerrors.ServiceUnavailable(ErrorCodeConfigUnavailable, "提示词审计配置暂不可用")
	}
	return PublicFromStorage(cloneStorageConfig(snapshot.storage), snapshot.active.RiskControlEnabled, snapshot.active.InvalidTokenEndpointIDs()), nil
}

func (m *ConfigManager) Save(ctx context.Context, req UpdateConfigRequest, actorID int64) (PublicConfig, error) {
	if m == nil || m.db == nil || m.encryptor == nil {
		return PublicConfig{}, errors.New("prompt audit config persistence unavailable")
	}
	if req.ExpectedConfigVersion < 1 {
		return PublicConfig{}, infraerrors.BadRequest("prompt_audit_expected_config_version_required", "必须提供有效的配置版本")
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return PublicConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, promptAuditConfigLockKey); err != nil {
		return PublicConfig{}, err
	}
	current := DefaultStorageConfig()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, SettingKeyPromptAuditConfig).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PublicConfig{}, err
	}
	if err == nil {
		current, err = ParseStorageConfig(raw)
		if err != nil {
			return PublicConfig{}, err
		}
	}
	if current.ConfigVersion != req.ExpectedConfigVersion {
		return PublicConfig{}, infraerrors.Conflict(ErrorCodeConfigConflict, "提示词审计配置已被其他管理员更新")
	}
	next, err := m.buildNextStorage(current, req, actorID)
	if err != nil {
		return PublicConfig{}, err
	}
	next.ConfigVersion = current.ConfigVersion + 1
	next.UpdatedAt = m.clock.Now()
	next.UpdatedBy = actorID
	next.ChangeSummary = changeSummary(next)
	// A disabled configuration with an existing ciphertext must remain editable
	// so administrators can clear it or change unrelated settings. Enabling
	// Prompt Audit still requires a fixed key, and buildNextStorage separately
	// rejects every newly supplied token when only a per-boot key is available.
	if next.Enabled {
		if err := m.validatePersistentEncryption(next); err != nil {
			return PublicConfig{}, err
		}
	}
	// Build the exact snapshot before committing storage. A configuration that
	// cannot decrypt or activate must never become the persisted desired state.
	riskControlEnabled := false
	var riskControlRaw string
	riskControlErr := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR SHARE`, SettingKeyRiskControl).Scan(&riskControlRaw)
	if riskControlErr != nil && !errors.Is(riskControlErr, sql.ErrNoRows) {
		return PublicConfig{}, riskControlErr
	}
	riskControlEnabled = strings.TrimSpace(riskControlRaw) == "true"
	active, err := ActiveFromStorage(next, riskControlEnabled, m.encryptor)
	if err != nil {
		return PublicConfig{}, err
	}
	rawNext, err := json.Marshal(next)
	if err != nil {
		return PublicConfig{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key,value,updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=EXCLUDED.updated_at`,
		SettingKeyPromptAuditConfig, string(rawNext)); err != nil {
		return PublicConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublicConfig{}, err
	}
	// Re-read the global gate after commit. It may have changed since the
	// transaction acquired its share lock; the installed snapshot must reflect
	// the latest observable value rather than a stale process-local value.
	if m.settings != nil {
		if values, getErr := m.settings.GetMultiple(ctx, []string{SettingKeyRiskControl}); getErr == nil {
			latestRiskControlEnabled := values[SettingKeyRiskControl] == "true"
			if latestRiskControlEnabled != riskControlEnabled {
				riskControlEnabled = latestRiskControlEnabled
				active, err = ActiveFromStorage(next, riskControlEnabled, m.encryptor)
				if err != nil {
					return PublicConfig{}, err
				}
			}
		}
	}
	previous := m.snapshot.Load()
	nextSnapshot := &activeConfigSnapshot{storage: cloneStorageConfig(next), active: cloneActiveConfig(active), loadedAt: m.clock.Now()}
	if m.installSnapshotIfNewer(nextSnapshot) {
		m.updateExpectedState(next.ConfigVersion, active.RiskControlEnabled && next.Enabled && next.BlockingEnabled)
		m.configUntrusted.Store(false)
		m.clearLoadError()
		m.logInvalidTokenEndpoints(previous, active)
	}
	LogInfo(EventConfigUpdated, map[string]any{
		"config_version": next.ConfigVersion, "status": "updated",
	})
	if m.redis != nil {
		if err := m.redis.Publish(ctx, ConfigInvalidationChannel, strconv.FormatInt(next.ConfigVersion, 10)).Err(); err != nil {
			LogWarn(EventConfigReloadDegraded, map[string]any{
				"config_version": next.ConfigVersion, "status": "degraded", "error_code": "config_invalidation_publish_failed",
			})
		}
	}
	return PublicFromStorage(next, active.RiskControlEnabled, active.InvalidTokenEndpointIDs()), nil
}

func (m *ConfigManager) buildNextStorage(current storageConfig, req UpdateConfigRequest, actorID int64) (storageConfig, error) {
	if err := validateUpdateConfigRequest(req); err != nil {
		return storageConfig{}, err
	}
	currentByID := make(map[string]StorageEndpoint, len(current.Endpoints))
	for _, endpoint := range current.Endpoints {
		currentByID[endpoint.ID] = endpoint
	}
	next := storageConfig{
		Enabled: req.Enabled, BlockingEnabled: req.BlockingEnabled, StorePassEvents: req.StorePassEvents,
		Strategy: strings.TrimSpace(req.Strategy), WorkerCount: req.WorkerCount,
		QueueCapacity: req.QueueCapacity, Scanners: append([]string(nil), req.Scanners...),
		AllGroups: req.AllGroups, GroupIDs: append([]int64(nil), req.GroupIDs...),
		ConfigVersion: current.ConfigVersion, UpdatedBy: actorID,
		Endpoints: make([]StorageEndpoint, 0, len(req.Endpoints)),
	}
	for _, endpoint := range req.Endpoints {
		baseURL, err := NormalizeBaseURL(endpoint.BaseURL)
		if err != nil {
			return storageConfig{}, err
		}
		stored := StorageEndpoint{
			ID: strings.TrimSpace(endpoint.ID), Name: strings.TrimSpace(endpoint.Name),
			Protocol: strings.TrimSpace(endpoint.Protocol), BaseURL: baseURL, Model: strings.TrimSpace(endpoint.Model),
			TimeoutMS: endpoint.TimeoutMS, InputLimit: endpoint.InputLimit, Enabled: endpoint.Enabled,
		}
		old, hadOld := currentByID[stored.ID]
		switch {
		case endpoint.ClearToken:
			stored.TokenCiphertext = ""
		case strings.TrimSpace(endpoint.Token) != "":
			if !m.persistentKeyConfigured {
				return storageConfig{}, infraerrors.BadRequest(ErrorCodeEncryptionKeyRequired,
					"未配置固定加密密钥，审计节点 Token 将在服务重启后失效。请先设置 TOTP_ENCRYPTION_KEY 环境变量（64 位十六进制）并重启服务")
			}
			ciphertext, err := m.encryptor.Encrypt(strings.TrimSpace(endpoint.Token))
			if err != nil {
				return storageConfig{}, fmt.Errorf("encrypt prompt audit endpoint token: %w", err)
			}
			stored.TokenCiphertext = ciphertext
		case hadOld:
			stored.TokenCiphertext = old.TokenCiphertext
		}
		next.Endpoints = append(next.Endpoints, stored)
	}
	normalizeStorageConfig(&next)
	if err := validateStorageConfig(next); err != nil {
		return storageConfig{}, err
	}
	return next, nil
}

func (m *ConfigManager) validatePersistentEncryption(cfg storageConfig) error {
	requiresPersistentKey := cfg.Enabled
	for _, endpoint := range cfg.Endpoints {
		if strings.TrimSpace(endpoint.TokenCiphertext) != "" {
			requiresPersistentKey = true
			break
		}
	}
	if !requiresPersistentKey || m.persistentKeyConfigured {
		return nil
	}
	return infraerrors.BadRequest(
		ErrorCodeEncryptionKey,
		"提示词审计需要固定的 TOTP_ENCRYPTION_KEY",
	)
}

func (m *ConfigManager) installSnapshotIfNewer(next *activeConfigSnapshot) bool {
	if m == nil || next == nil {
		return false
	}
	for {
		current := m.snapshot.Load()
		if current != nil && current.active.ConfigVersion >= next.active.ConfigVersion {
			return false
		}
		if m.snapshot.CompareAndSwap(current, next) {
			return true
		}
	}
}

func (m *ConfigManager) installSnapshotIfCurrentOrNewer(next *activeConfigSnapshot) bool {
	if m == nil || next == nil {
		return false
	}
	for {
		current := m.snapshot.Load()
		if current != nil && current.active.ConfigVersion > next.active.ConfigVersion {
			return false
		}
		if m.snapshot.CompareAndSwap(current, next) {
			return true
		}
	}
}

func (m *ConfigManager) RuntimeState() (expected int64, active int64, loadedAt *time.Time, loadError string) {
	if m == nil {
		return 1, 0, nil, "config_manager_unavailable"
	}
	m.expectedMu.RLock()
	expected = m.expected.Load()
	m.expectedMu.RUnlock()
	if expected < 1 {
		expected = 1
	}
	if snapshot := m.snapshot.Load(); snapshot != nil {
		active = snapshot.active.ConfigVersion
		value := snapshot.loadedAt
		loadedAt = &value
	}
	m.stateMu.RLock()
	loadError = m.lastLoadError
	m.stateMu.RUnlock()
	return
}

func (m *ConfigManager) Encrypt(value string) (string, error) {
	if m == nil || m.encryptor == nil || !m.persistentKeyConfigured {
		return "", infraerrors.BadRequest(ErrorCodeEncryptionKey, "提示词审计需要固定的 TOTP_ENCRYPTION_KEY")
	}
	return m.encryptor.Encrypt(value)
}

func (m *ConfigManager) Decrypt(value string) (string, error) {
	if m == nil || m.encryptor == nil || !m.persistentKeyConfigured {
		return "", infraerrors.BadRequest(ErrorCodeEncryptionKey, "提示词审计需要固定的 TOTP_ENCRYPTION_KEY")
	}
	return m.encryptor.Decrypt(value)
}

func (m *ConfigManager) observeExpectedState(raw string, riskControlEnabled bool) {
	if m == nil {
		return
	}
	if strings.TrimSpace(raw) == "" {
		m.updateExpectedState(1, false)
		return
	}
	var intent struct {
		Enabled         bool  `json:"enabled"`
		BlockingEnabled bool  `json:"blocking_enabled"`
		ConfigVersion   int64 `json:"config_version"`
	}
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return
	}
	if intent.ConfigVersion < 1 {
		intent.ConfigVersion = 1
	}
	m.updateExpectedState(intent.ConfigVersion, riskControlEnabled && intent.Enabled && intent.BlockingEnabled)
}

func (m *ConfigManager) updateExpectedState(version int64, blocking bool) bool {
	if m == nil {
		return false
	}
	if version < 1 {
		version = 1
	}
	m.expectedMu.Lock()
	defer m.expectedMu.Unlock()
	if version < m.expected.Load() {
		return false
	}
	m.expected.Store(version)
	m.expectedBlocking.Store(blocking)
	return true
}

func (m *ConfigManager) observeExpectedVersion(version int64) {
	if m == nil || version < 1 {
		return
	}
	m.expectedMu.Lock()
	if version > m.expected.Load() {
		m.expected.Store(version)
	}
	m.expectedMu.Unlock()
}

func (m *ConfigManager) refreshLoop(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.Reload(ctx); err != nil {
				LogWarn(EventConfigReloadDegraded, map[string]any{"status": "degraded", "error_code": "config_ttl_reload_failed"})
			}
		}
	}
}

func (m *ConfigManager) subscribeLoop(ctx context.Context) {
	defer m.wg.Done()
	pubsub := m.redis.Subscribe(ctx, ConfigInvalidationChannel)
	defer func() { _ = pubsub.Close() }()
	channel := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-channel:
			if !ok {
				return
			}
			version, err := strconv.ParseInt(strings.TrimSpace(message.Payload), 10, 64)
			if err != nil || version < 1 {
				continue
			}
			m.observeExpectedVersion(version)
			if err := m.Reload(ctx); err != nil {
				// A newer published version failed to activate. Until reload
				// succeeds, do not keep serving a potentially stale weaker mode.
				if active, ok := m.Active(); !ok || active.ConfigVersion < version {
					m.markConfigUntrusted()
				}
				LogWarn(EventConfigReloadDegraded, map[string]any{
					"config_version": version, "status": "degraded", "error_code": "config_invalidation_reload_failed",
				})
			}
		}
	}
}

func (m *ConfigManager) recordLoadError(_ error) {
	if m == nil {
		return
	}
	now := m.clock.Now()
	m.stateMu.Lock()
	m.lastLoadError = stableErrorMessage("config_load_failed")
	m.lastErrorAt = &now
	m.stateMu.Unlock()
}

func (m *ConfigManager) clearLoadError() {
	m.stateMu.Lock()
	m.lastLoadError = ""
	m.lastErrorAt = nil
	m.stateMu.Unlock()
}

func cloneStorageConfig(cfg storageConfig) storageConfig {
	cfg.Scanners = append([]string(nil), cfg.Scanners...)
	cfg.GroupIDs = append([]int64(nil), cfg.GroupIDs...)
	cfg.Endpoints = append([]StorageEndpoint(nil), cfg.Endpoints...)
	return cfg
}

func cloneActiveConfig(cfg ActiveConfig) ActiveConfig {
	cfg.Scanners = append([]string(nil), cfg.Scanners...)
	cfg.GroupIDs = append([]int64(nil), cfg.GroupIDs...)
	cfg.Endpoints = append([]ActiveEndpoint(nil), cfg.Endpoints...)
	return cfg
}
