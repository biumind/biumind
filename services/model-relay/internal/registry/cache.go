// Cache is a read-through, LISTEN/NOTIFY-invalidated layer over Store.
//
// The runtime path (per LLM request) wants a sub-millisecond answer to
// "what's the active channel set for model X?", which Postgres can give
// but only at a few hundred μs RTT × N requests/s. We cache:
//
//   * models keyed by code            (resolver entry point)
//   * channels keyed by model_id      (sorted, active-only)
//   * credentials keyed by id         (decryption gets the bytes)
//   * fx_rates keyed by "from/to"     (per-request settlement)
//
// Pricing is intentionally NOT cached — usage settlement reads it once
// per finished request and benefits from being fully consistent.
//
// Invalidation: a single goroutine LISTENs on `model_relay_config_changed`
// and flips per-table dirty bits on every NOTIFY. The next read for that
// sub-cache reloads from DB. Coarse but correct: 8-table mutation rate
// is admin-only (handful per day), reload cost < 5ms — too cheap to
// optimise further at MVP scope. TTL 60s is the floor in case NOTIFY
// is missed (connection drop between mutation and reconnect).
//
// Concurrency model: a single sync.RWMutex guards every sub-cache.
// singleflight collapses concurrent reloads of the same sub-cache so
// a thundering herd can't run 100 List queries.

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

// CacheConfig governs reload cadence and the LISTEN reconnect loop.
type CacheConfig struct {
	// TTL is the floor reload interval — every entry is forced stale
	// after this long even without a NOTIFY (covers missed events
	// during connection drops). 60s default per Dev Plan §6.1.
	TTL time.Duration
	// ReconnectDelay is the backoff between LISTEN reconnect attempts.
	ReconnectDelay time.Duration
	Logger         *slog.Logger
}

func (c *CacheConfig) defaults() {
	if c.TTL == 0 {
		c.TTL = 60 * time.Second
	}
	if c.ReconnectDelay == 0 {
		c.ReconnectDelay = 2 * time.Second
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// NotifyChannelName matches the trigger function in
// services/model-relay/migrations/00002_notify_triggers.sql.
const NotifyChannelName = "model_relay_config_changed"

// Cache wraps a Store with read-through in-memory caches. Construct
// with NewCache; call Start to begin LISTEN; Close on shutdown.
type Cache struct {
	store *Store
	cfg   CacheConfig

	mu sync.RWMutex

	models         map[uuid.UUID]*Model
	modelsByCode   map[string]*Model
	modelsLoadedAt time.Time
	modelsDirty    bool

	providers         map[uuid.UUID]*Provider
	providersLoadedAt time.Time
	providersDirty    bool

	channelsByModel  map[uuid.UUID][]Channel
	channelsLoadedAt time.Time
	channelsDirty    bool

	credentials       map[uuid.UUID]*Credential
	credentialsLoadAt time.Time
	credentialsDirty  bool

	fxRates       map[string]float64
	fxRatesLoadAt time.Time
	fxRatesDirty  bool

	groupBindings       map[uuid.UUID][]uuid.UUID // model_id → group_ids
	groupBindingsLoadAt time.Time
	groupBindingsDirty  bool

	sf singleflight.Group

	cancel context.CancelFunc
	done   chan struct{}
}

// NewCache wires a Store + config. Cache stays cold until Start runs.
func NewCache(store *Store, cfg CacheConfig) *Cache {
	cfg.defaults()
	return &Cache{
		store: store,
		cfg:   cfg,
		// Sentinel state: dirty=true forces first load on first read.
		modelsDirty:        true,
		channelsDirty:      true,
		credentialsDirty:   true,
		fxRatesDirty:       true,
		groupBindingsDirty: true,
		providersDirty:     true,
		done:               make(chan struct{}),
	}
}

// Start launches the LISTEN goroutine. Blocks until the listener has
// actually subscribed (so callers can rely on subsequent NOTIFY events
// being delivered). Returns a non-nil error only on initial connection
// failure; subsequent reconnects log and retry.
func (c *Cache) Start(ctx context.Context) error {
	listenerCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	// Probe — confirm we can acquire a dedicated conn and LISTEN. If
	// this fails, caller (main.go) decides whether to retry or bail.
	conn, err := c.store.Pool.Acquire(listenerCtx)
	if err != nil {
		cancel()
		return fmt.Errorf("cache: acquire conn: %w", err)
	}
	if _, err := conn.Exec(listenerCtx, "LISTEN "+NotifyChannelName); err != nil {
		conn.Release()
		cancel()
		return fmt.Errorf("cache: LISTEN: %w", err)
	}

	go c.listenLoop(listenerCtx, conn)
	return nil
}

// Close stops the listener and waits for the goroutine to exit. Safe
// to call multiple times.
func (c *Cache) Close() {
	if c.cancel == nil {
		return
	}
	c.cancel()
	<-c.done
}

// ─── Public read API ──────────────────────────────────────────────

// GetModelByCode returns the active model with that code, or ErrNotFound.
// The hot path: ModelResolver calls this on every LLM request.
func (c *Cache) GetModelByCode(ctx context.Context, code string) (*Model, error) {
	if err := c.ensureModels(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	m, ok := c.modelsByCode[code]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cache.get_model_by_code: %w", ErrNotFound)
	}
	return m, nil
}

func (c *Cache) GetModel(ctx context.Context, id uuid.UUID) (*Model, error) {
	if err := c.ensureModels(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	m, ok := c.models[id]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cache.get_model: %w", ErrNotFound)
	}
	return m, nil
}

// DefaultChatModel returns the admin-designated default chat model
// (models.is_default_chat = true), or ErrNotFound when none is set.
// A deactivated default (status != active) is treated as "no default" —
// callers (internalapi → brain ChatRunner) fall back accordingly.
// Reuses the models sub-cache; is_default_chat changes ride the same
// models NOTIFY trigger, so invalidation needs nothing extra.
func (c *Cache) DefaultChatModel(ctx context.Context) (*Model, error) {
	if err := c.ensureModels(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range c.models {
		if m.IsDefaultChat && m.Status == StatusActive {
			return m, nil
		}
	}
	return nil, fmt.Errorf("cache.default_chat_model: %w", ErrNotFound)
}

// ChannelsForModel returns the active channel set for a model, already
// sorted (priority DESC, weight DESC). Strategy.Pick consumes this
// directly. Empty slice when no active channels — caller decides whether
// that's a 503 or a "model misconfigured" 500.
func (c *Cache) ChannelsForModel(ctx context.Context, modelID uuid.UUID) ([]Channel, error) {
	if err := c.ensureChannels(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	chans := c.channelsByModel[modelID]
	c.mu.RUnlock()
	// Hand back a copy so caller mutations (e.g. retry exclusion list)
	// don't poison the cache.
	out := make([]Channel, len(chans))
	copy(out, chans)
	return out, nil
}

// GetCredential returns the (still-encrypted) credential. Caller hits
// envelope.Decrypt before passing the plaintext to provider adaptor.
func (c *Cache) GetCredential(ctx context.Context, id uuid.UUID) (*Credential, error) {
	if err := c.ensureCredentials(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	cred, ok := c.credentials[id]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cache.get_credential: %w", ErrNotFound)
	}
	return cred, nil
}

// FxRate returns the rate for a (from, to) pair. self-reflexive returns
// 1.0 directly without DB hit (the seed row has rate=1 anyway, but
// short-circuiting saves a map lookup).
func (c *Cache) FxRate(ctx context.Context, from, to Currency) (float64, error) {
	if from == to {
		return 1.0, nil
	}
	if err := c.ensureFxRates(ctx); err != nil {
		return 0, err
	}
	key := string(from) + "/" + string(to)
	c.mu.RLock()
	rate, ok := c.fxRates[key]
	c.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("cache.fx_rate %s: %w", key, ErrNotFound)
	}
	return rate, nil
}

// GetProvider returns the provider record from the in-memory cache.
// Used by ModelResolver to surface the protocol on the hot path
// without an extra DB round-trip.
func (c *Cache) GetProvider(ctx context.Context, id uuid.UUID) (*Provider, error) {
	if err := c.ensureProviders(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	p, ok := c.providers[id]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cache.get_provider: %w", ErrNotFound)
	}
	return p, nil
}

// GroupsForModel returns the group_ids the given model is bound to.
// Used by ModelResolver as part of the visibility filter.
func (c *Cache) GroupsForModel(ctx context.Context, modelID uuid.UUID) ([]uuid.UUID, error) {
	if err := c.ensureGroupBindings(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	gids := c.groupBindings[modelID]
	c.mu.RUnlock()
	out := make([]uuid.UUID, len(gids))
	copy(out, gids)
	return out, nil
}

// ─── Reload logic (singleflight-collapsed) ────────────────────────

func (c *Cache) ensureModels(ctx context.Context) error {
	c.mu.RLock()
	stale := c.modelsDirty || time.Since(c.modelsLoadedAt) > c.cfg.TTL
	c.mu.RUnlock()
	if !stale {
		return nil
	}
	_, err, _ := c.sf.Do("models", func() (any, error) { return nil, c.reloadModels(ctx) })
	return err
}

func (c *Cache) reloadModels(ctx context.Context) error {
	all, err := c.store.Models.List(ctx, ModelFilter{})
	if err != nil {
		return fmt.Errorf("cache.reload_models: %w", err)
	}
	byID := make(map[uuid.UUID]*Model, len(all))
	byCode := make(map[string]*Model, len(all))
	for i := range all {
		m := all[i] // copy out of slice
		byID[m.ID] = &m
		byCode[m.Code] = &m
	}
	c.mu.Lock()
	c.models = byID
	c.modelsByCode = byCode
	c.modelsLoadedAt = time.Now()
	c.modelsDirty = false
	c.mu.Unlock()
	return nil
}

func (c *Cache) ensureChannels(ctx context.Context) error {
	c.mu.RLock()
	stale := c.channelsDirty || time.Since(c.channelsLoadedAt) > c.cfg.TTL
	c.mu.RUnlock()
	if !stale {
		return nil
	}
	_, err, _ := c.sf.Do("channels", func() (any, error) { return nil, c.reloadChannels(ctx) })
	return err
}

func (c *Cache) reloadChannels(ctx context.Context) error {
	// Single query loads all active channels. The natural pgx ORDER BY
	// (priority DESC, weight DESC, id ASC) means we group-by-model
	// in code and the per-group slice is already sorted.
	all, err := c.store.Channels.List(ctx, ChannelFilter{Status: StatusActive})
	if err != nil {
		return fmt.Errorf("cache.reload_channels: %w", err)
	}
	byModel := make(map[uuid.UUID][]Channel)
	for _, ch := range all {
		byModel[ch.ModelID] = append(byModel[ch.ModelID], ch)
	}
	// Per-model slice is already in (priority DESC, weight DESC) thanks
	// to ChannelRepo.List's ORDER BY, but defensively re-sort in case
	// future filter changes break that assumption.
	for k, v := range byModel {
		sort.SliceStable(v, func(i, j int) bool {
			if v[i].Priority != v[j].Priority {
				return v[i].Priority > v[j].Priority
			}
			return v[i].Weight > v[j].Weight
		})
		byModel[k] = v
	}
	c.mu.Lock()
	c.channelsByModel = byModel
	c.channelsLoadedAt = time.Now()
	c.channelsDirty = false
	c.mu.Unlock()
	return nil
}

func (c *Cache) ensureCredentials(ctx context.Context) error {
	c.mu.RLock()
	stale := c.credentialsDirty || time.Since(c.credentialsLoadAt) > c.cfg.TTL
	c.mu.RUnlock()
	if !stale {
		return nil
	}
	_, err, _ := c.sf.Do("credentials", func() (any, error) { return nil, c.reloadCredentials(ctx) })
	return err
}

func (c *Cache) reloadCredentials(ctx context.Context) error {
	// Only active credentials — disabled / invalid ones can't serve
	// requests, no point caching their encrypted bytes.
	all, err := c.store.Credentials.List(ctx, CredentialFilter{Status: StatusActive})
	if err != nil {
		return fmt.Errorf("cache.reload_credentials: %w", err)
	}
	byID := make(map[uuid.UUID]*Credential, len(all))
	for i := range all {
		cred := all[i]
		byID[cred.ID] = &cred
	}
	c.mu.Lock()
	c.credentials = byID
	c.credentialsLoadAt = time.Now()
	c.credentialsDirty = false
	c.mu.Unlock()
	return nil
}

func (c *Cache) ensureFxRates(ctx context.Context) error {
	c.mu.RLock()
	stale := c.fxRatesDirty || time.Since(c.fxRatesLoadAt) > c.cfg.TTL
	c.mu.RUnlock()
	if !stale {
		return nil
	}
	_, err, _ := c.sf.Do("fx_rates", func() (any, error) { return nil, c.reloadFxRates(ctx) })
	return err
}

func (c *Cache) reloadFxRates(ctx context.Context) error {
	all, err := c.store.FxRates.List(ctx)
	if err != nil {
		return fmt.Errorf("cache.reload_fx_rates: %w", err)
	}
	by := make(map[string]float64, len(all))
	for _, r := range all {
		by[string(r.FromCurrency)+"/"+string(r.ToCurrency)] = r.Rate
	}
	c.mu.Lock()
	c.fxRates = by
	c.fxRatesLoadAt = time.Now()
	c.fxRatesDirty = false
	c.mu.Unlock()
	return nil
}

func (c *Cache) ensureProviders(ctx context.Context) error {
	c.mu.RLock()
	stale := c.providersDirty || time.Since(c.providersLoadedAt) > c.cfg.TTL
	c.mu.RUnlock()
	if !stale {
		return nil
	}
	_, err, _ := c.sf.Do("providers", func() (any, error) { return nil, c.reloadProviders(ctx) })
	return err
}

func (c *Cache) reloadProviders(ctx context.Context) error {
	all, err := c.store.Providers.List(ctx, ProviderFilter{})
	if err != nil {
		return fmt.Errorf("cache.reload_providers: %w", err)
	}
	by := make(map[uuid.UUID]*Provider, len(all))
	for i := range all {
		p := all[i]
		by[p.ID] = &p
	}
	c.mu.Lock()
	c.providers = by
	c.providersLoadedAt = time.Now()
	c.providersDirty = false
	c.mu.Unlock()
	return nil
}

func (c *Cache) ensureGroupBindings(ctx context.Context) error {
	c.mu.RLock()
	stale := c.groupBindingsDirty || time.Since(c.groupBindingsLoadAt) > c.cfg.TTL
	c.mu.RUnlock()
	if !stale {
		return nil
	}
	_, err, _ := c.sf.Do("group_bindings", func() (any, error) { return nil, c.reloadGroupBindings(ctx) })
	return err
}

func (c *Cache) reloadGroupBindings(ctx context.Context) error {
	const q = `SELECT model_id, group_id FROM model_relay.model_group_bindings`
	rows, err := c.store.Pool.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("cache.reload_group_bindings: %w", err)
	}
	defer rows.Close()
	by := make(map[uuid.UUID][]uuid.UUID)
	for rows.Next() {
		var mid, gid uuid.UUID
		if err := rows.Scan(&mid, &gid); err != nil {
			return fmt.Errorf("cache.reload_group_bindings.scan: %w", err)
		}
		by[mid] = append(by[mid], gid)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.groupBindings = by
	c.groupBindingsLoadAt = time.Now()
	c.groupBindingsDirty = false
	c.mu.Unlock()
	return nil
}

// ─── LISTEN loop ──────────────────────────────────────────────────

// listenLoop runs a single LISTEN connection forever. On any error it
// releases the conn, sleeps cfg.ReconnectDelay, re-acquires + LISTENs
// again. Because every reload checks the dirty bit, missed events
// during a reconnect window are absorbed by TTL — the resolver might
// see a stale value for at most cfg.TTL after a NOTIFY storm.
func (c *Cache) listenLoop(ctx context.Context, conn *pgxpool.Conn) {
	defer close(c.done)
	defer func() {
		if conn != nil {
			conn.Release()
		}
	}()

	for {
		notif, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				c.cfg.Logger.Info("model_relay cache: listener stopped")
				return
			}
			c.cfg.Logger.Warn("model_relay cache: listener error, reconnecting",
				"err", err.Error())
			conn.Release()
			conn = nil

			select {
			case <-ctx.Done():
				return
			case <-time.After(c.cfg.ReconnectDelay):
			}
			conn = c.reconnect(ctx)
			if conn == nil {
				return // ctx done while reconnecting
			}
			continue
		}
		c.handleNotification(notif)
	}
}

func (c *Cache) reconnect(ctx context.Context) *pgxpool.Conn {
	for {
		conn, err := c.store.Pool.Acquire(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			c.cfg.Logger.Warn("model_relay cache: acquire failed", "err", err.Error())
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(c.cfg.ReconnectDelay):
			}
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN "+NotifyChannelName); err != nil {
			conn.Release()
			c.cfg.Logger.Warn("model_relay cache: re-LISTEN failed", "err", err.Error())
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(c.cfg.ReconnectDelay):
			}
			continue
		}
		c.cfg.Logger.Info("model_relay cache: listener reconnected")
		// Force a full re-load — events during the disconnect window
		// were missed.
		c.markAllDirty()
		return conn
	}
}

func (c *Cache) markAllDirty() {
	c.mu.Lock()
	c.modelsDirty = true
	c.channelsDirty = true
	c.credentialsDirty = true
	c.fxRatesDirty = true
	c.groupBindingsDirty = true
	c.providersDirty = true
	c.mu.Unlock()
}

// notifyPayload mirrors the trigger function's jsonb_build_object call
// in 00002_notify_triggers.sql.
type notifyPayload struct {
	Table string `json:"table"`
	ID    string `json:"id"`
	Op    string `json:"op"`
}

// handleNotification routes a NOTIFY to the right sub-cache dirty bit.
// Coarse-grained: any row change in a sub-cache table flips the whole
// sub-cache dirty. The next read triggers reload (singleflight collapses
// concurrent reads).
//
// Note: the trigger formats payload via jsonb::text which yields keys
// with single spaces (`{"id": "...", "op": "...", "table": "..."}`).
// We use encoding/json to parse so the matching is whitespace-immune.
func (c *Cache) handleNotification(notif *pgconn.Notification) {
	var p notifyPayload
	if err := json.Unmarshal([]byte(notif.Payload), &p); err != nil {
		c.cfg.Logger.Warn("model_relay cache: invalid NOTIFY payload",
			"payload", notif.Payload, "err", err.Error())
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch p.Table {
	case "models":
		c.modelsDirty = true
	case "channels":
		// Channels reference credentials; flipping channels dirty also
		// covers the case where a channel's credential changed.
		c.channelsDirty = true
	case "credentials":
		c.credentialsDirty = true
		// Channels embed credential_id but not the bytes; not strictly
		// required to invalidate channels here.
	case "providers":
		c.providersDirty = true
	case "fx_rates":
		c.fxRatesDirty = true
	case "model_groups", "model_group_bindings":
		c.groupBindingsDirty = true
		// Visibility might change → also re-evaluate models cache so
		// a downgraded min_plan / new group binding is reflected.
		c.modelsDirty = true
	case "user_group_memberships":
		// Per-user state, not cached at this layer.
	default:
		c.cfg.Logger.Debug("model_relay cache: unknown NOTIFY table",
			"table", p.Table)
	}
}
