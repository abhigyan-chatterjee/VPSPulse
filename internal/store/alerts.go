package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// RedactedSentinel marks redacted secret values in API responses and incoming PATCH payloads.
const RedactedSentinel = "__REDACTED__"

var telegramBotTokenRe = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,}$`)

// ChannelEvents controls which alert categories are sent to a notification channel.
type ChannelEvents struct {
	Offline  bool `json:"offline"`
	Service  bool `json:"service"`
	Resource bool `json:"resource"`
	TLS      bool `json:"tls"`
}

// NotificationChannel represents a configured alert webhook destination.
type NotificationChannel struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"` // "discord", "telegram", "slack", "smtp", "webhook"
	Config    map[string]string `json:"config"`
	Events    ChannelEvents     `json:"events"`
	Enabled   bool              `json:"enabled"`
	CreatedAt int64             `json:"created_at"`
}

// ChannelPatch represents partial updates for PATCH /api/v1/channels/{id}.
type ChannelPatch struct {
	Name    *string           `json:"name"`
	Type    *string           `json:"type"`
	Config  map[string]string `json:"config"`
	Events  *ChannelEvents    `json:"events"`
	Enabled *bool             `json:"enabled"`
}

// Incident records an outage, failure, threshold breach, or recovery event.
type Incident struct {
	ID          string  `json:"id"`
	IncidentKey string  `json:"incident_key"`
	VPSID       *string `json:"vps_id"`
	Severity    string  `json:"severity"` // "critical", "warning", "info"
	Title       string  `json:"title"`
	Detail      string  `json:"detail"`
	Status      string  `json:"status"` // "open", "acknowledged", "resolved"
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
	ResolvedAt  int64   `json:"resolved_at"`
}

// ValidateChannel validates notification channel type and endpoint fields.
func ValidateChannel(typ string, cfg map[string]string) error {
	switch typ {
	case "discord", "slack":
		raw := strings.TrimSpace(cfg["webhook_url"])
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("%s webhook_url must be a valid https:// URL", typ)
		}
		return nil
	case "telegram":
		tok := strings.TrimSpace(cfg["bot_token"])
		chatID := strings.TrimSpace(cfg["chat_id"])
		if !telegramBotTokenRe.MatchString(tok) {
			return errors.New("invalid telegram bot_token format")
		}
		if chatID == "" {
			return errors.New("telegram chat_id is required")
		}
		return nil
	case "smtp":
		if strings.TrimSpace(cfg["host"]) == "" || strings.TrimSpace(cfg["from"]) == "" || strings.TrimSpace(cfg["to"]) == "" {
			return errors.New("smtp requires host, from, and to")
		}
		return nil
	case "webhook":
		raw := strings.TrimSpace(cfg["url"])
		if raw == "" {
			raw = strings.TrimSpace(cfg["webhook_url"])
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("webhook url must be a valid http:// or https:// URL")
		}
		return nil
	default:
		return fmt.Errorf("unsupported channel type %q", typ)
	}
}

// ListChannels returns all configured notification channels.
func (s *Store) ListChannels() ([]NotificationChannel, error) {
	rows, err := s.db.Query(`SELECT id, name, type, config_json, events_json, enabled, created_at FROM notification_channels ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []NotificationChannel{}
	for rows.Next() {
		var ch NotificationChannel
		var cfgJSON, evJSON string
		var enabled int
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Type, &cfgJSON, &evJSON, &enabled, &ch.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(cfgJSON), &ch.Config)
		if ch.Config == nil {
			ch.Config = map[string]string{}
		}
		_ = json.Unmarshal([]byte(evJSON), &ch.Events)
		ch.Enabled = enabled == 1
		out = append(out, ch)
	}
	return out, rows.Err()
}

// GetChannel fetches a single notification channel by ID.
func (s *Store) GetChannel(id string) (*NotificationChannel, error) {
	var ch NotificationChannel
	var cfgJSON, evJSON string
	var enabled int
	err := s.db.QueryRow(
		`SELECT id, name, type, config_json, events_json, enabled, created_at FROM notification_channels WHERE id = ?`,
		id,
	).Scan(&ch.ID, &ch.Name, &ch.Type, &cfgJSON, &evJSON, &enabled, &ch.CreatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(cfgJSON), &ch.Config)
	if ch.Config == nil {
		ch.Config = map[string]string{}
	}
	_ = json.Unmarshal([]byte(evJSON), &ch.Events)
	ch.Enabled = enabled == 1
	return &ch, nil
}

// CreateChannel stores a new notification channel.
func (s *Store) CreateChannel(ch NotificationChannel) (*NotificationChannel, error) {
	ch.Name = strings.TrimSpace(ch.Name)
	ch.Type = strings.ToLower(strings.TrimSpace(ch.Type))
	if ch.Name == "" || ch.Type == "" {
		return nil, errors.New("channel name and type are required")
	}
	if ch.Config == nil {
		ch.Config = map[string]string{}
	}
	if err := ValidateChannel(ch.Type, ch.Config); err != nil {
		return nil, err
	}
	ch.ID = newID("chn")
	ch.CreatedAt = time.Now().Unix()
	cfgJSON, _ := json.Marshal(ch.Config)
	evJSON, _ := json.Marshal(ch.Events)
	enabled := 0
	if ch.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO notification_channels (id, name, type, config_json, events_json, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ch.ID, ch.Name, ch.Type, string(cfgJSON), string(evJSON), enabled, ch.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return s.GetChannel(ch.ID)
}

func isSecretKey(k string) bool {
	return k == "password" || k == "bot_token" || k == "secret" || k == "webhook_url" || k == "url"
}

// UpdateChannel applies a partial ChannelPatch, preserving redacted secrets when omitted or marked redacted.
func (s *Store) UpdateChannel(id string, patch ChannelPatch) (*NotificationChannel, error) {
	cur, err := s.GetChannel(id)
	if err != nil {
		return nil, err
	}
	prevType := cur.Type
	if patch.Name != nil {
		trimmed := strings.TrimSpace(*patch.Name)
		if trimmed == "" {
			return nil, errors.New("channel name cannot be empty")
		}
		cur.Name = trimmed
	}
	if patch.Type != nil && strings.TrimSpace(*patch.Type) != "" {
		cur.Type = strings.ToLower(strings.TrimSpace(*patch.Type))
	}
	if patch.Config != nil {
		merged := map[string]string{}
		if cur.Type == prevType {
			for k, v := range cur.Config {
				merged[k] = v
			}
		}
		for k, v := range patch.Config {
			if isSecretKey(k) && (v == "" || v == RedactedSentinel || strings.Contains(v, "••••")) {
				continue
			}
			merged[k] = v
		}
		cur.Config = merged
	}
	if err := ValidateChannel(cur.Type, cur.Config); err != nil {
		return nil, err
	}
	if patch.Events != nil {
		cur.Events = *patch.Events
	}
	if patch.Enabled != nil {
		cur.Enabled = *patch.Enabled
	}

	cfgJSON, _ := json.Marshal(cur.Config)
	evJSON, _ := json.Marshal(cur.Events)
	enabled := 0
	if cur.Enabled {
		enabled = 1
	}
	_, err = s.db.Exec(
		`UPDATE notification_channels SET name=?, type=?, config_json=?, events_json=?, enabled=? WHERE id=?`,
		cur.Name, cur.Type, string(cfgJSON), string(evJSON), enabled, id,
	)
	if err != nil {
		return nil, err
	}
	return s.GetChannel(id)
}

// DeleteChannel deletes a notification channel.
func (s *Store) DeleteChannel(id string) error {
	res, err := s.db.Exec(`DELETE FROM notification_channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListIncidents returns recent incidents optionally filtered by status.
func (s *Store) ListIncidents(statusFilter string, limit int) ([]Incident, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, incident_key, vps_id, severity, title, detail, status, created_at, updated_at, resolved_at FROM incidents`
	var args []any
	if statusFilter != "" && statusFilter != "all" {
		query += ` WHERE status = ?`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Incident{}
	for rows.Next() {
		var inc Incident
		var vpsID sql.NullString
		if err := rows.Scan(&inc.ID, &inc.IncidentKey, &vpsID, &inc.Severity, &inc.Title, &inc.Detail, &inc.Status, &inc.CreatedAt, &inc.UpdatedAt, &inc.ResolvedAt); err != nil {
			return nil, err
		}
		if vpsID.Valid {
			v := vpsID.String
			inc.VPSID = &v
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// CreateOrUpdateIncident creates an open incident if none exists for incidentKey, returning (inc, isNew, error).
func (s *Store) CreateOrUpdateIncident(incidentKey, severity string, vpsID *string, title, detail string) (*Incident, bool, error) {
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var existingID string
	err = tx.QueryRow(
		`SELECT id FROM incidents WHERE incident_key = ? AND status IN ('open', 'acknowledged') ORDER BY created_at DESC LIMIT 1`,
		incidentKey,
	).Scan(&existingID)
	if err == nil {
		if _, err := tx.Exec(
			`UPDATE incidents SET severity=?, title=?, detail=?, updated_at=? WHERE id=?`,
			severity, title, detail, now, existingID,
		); err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		inc, err := s.getIncident(existingID)
		return inc, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	id := newID("inc")
	var vpsArg any
	if vpsID != nil && *vpsID != "" {
		vpsArg = *vpsID
	}
	if _, err := tx.Exec(
		`INSERT INTO incidents (id, incident_key, vps_id, severity, title, detail, status, created_at, updated_at, resolved_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?, 0)`,
		id, incidentKey, vpsArg, severity, title, detail, now, now,
	); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	inc, err := s.getIncident(id)
	return inc, true, err
}

// ResolveIncidentByKey marks any open/acknowledged incident for incidentKey as resolved.
func (s *Store) ResolveIncidentByKey(incidentKey, detail string) (*Incident, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var existingID string
	err = tx.QueryRow(
		`SELECT id FROM incidents WHERE incident_key = ? AND status IN ('open', 'acknowledged') ORDER BY created_at DESC LIMIT 1`,
		incidentKey,
	).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(
		`UPDATE incidents SET status = 'resolved', detail = ?, updated_at = ?, resolved_at = ? WHERE id = ?`,
		detail, now, now, existingID,
	); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	inc, err := s.getIncident(existingID)
	return inc, true, err
}

// ResolveStaleIncidentsByPrefix resolves any open/acknowledged incidents whose key starts with prefix and is not in activeKeys.
func (s *Store) ResolveStaleIncidentsByPrefix(prefix string, activeKeys map[string]bool, detail string) ([]Incident, error) {
	runeLen := utf8.RuneCountInString(prefix)
	rows, err := s.db.Query(
		`SELECT id, incident_key FROM incidents
		 WHERE status IN ('open', 'acknowledged') AND SUBSTR(incident_key, 1, ?) = ?`,
		runeLen, prefix,
	)
	if err != nil {
		return nil, err
	}
	var toResolve []string
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !activeKeys[key] {
			toResolve = append(toResolve, id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	if len(toResolve) == 0 {
		return nil, nil
	}

	now := time.Now().Unix()
	var resolved []Incident
	for _, id := range toResolve {
		if _, err := s.db.Exec(
			`UPDATE incidents SET status = 'resolved', detail = ?, updated_at = ?, resolved_at = ? WHERE id = ?`,
			detail, now, now, id,
		); err != nil {
			return resolved, err
		}
		if inc, err := s.getIncident(id); err == nil {
			resolved = append(resolved, *inc)
		}
	}
	return resolved, nil
}

// ResolveIncidentsByVPS resolves all open/acknowledged incidents tied to vpsID before deletion.
func (s *Store) ResolveIncidentsByVPS(vpsID, detail string) ([]Incident, error) {
	rows, err := s.db.Query(
		`SELECT id FROM incidents WHERE vps_id = ? AND status IN ('open', 'acknowledged')`,
		vpsID,
	)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if len(ids) == 0 {
		return nil, nil
	}
	now := time.Now().Unix()
	var resolved []Incident
	for _, id := range ids {
		if _, err := s.db.Exec(
			`UPDATE incidents SET status = 'resolved', detail = ?, updated_at = ?, resolved_at = ? WHERE id = ?`,
			detail, now, now, id,
		); err == nil {
			if inc, err := s.getIncident(id); err == nil {
				resolved = append(resolved, *inc)
			}
		}
	}
	return resolved, nil
}

// ResolveIncidentsByProbe resolves open/acknowledged probe-down and probe-tls incidents for probeID.
func (s *Store) ResolveIncidentsByProbe(probeID, detail string) ([]Incident, error) {
	var out []Incident
	for _, key := range []string{"probe-down:" + probeID, "probe-tls:" + probeID} {
		if inc, ok, err := s.ResolveIncidentByKey(key, detail); err == nil && ok && inc != nil {
			out = append(out, *inc)
		}
	}
	return out, nil
}

// AcknowledgeIncident transitions an open incident to acknowledged.
func (s *Store) AcknowledgeIncident(id string) error {
	res, err := s.db.Exec(
		`UPDATE incidents SET status = 'acknowledged', updated_at = ? WHERE id = ? AND status = 'open'`,
		time.Now().Unix(), id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) getIncident(id string) (*Incident, error) {
	var inc Incident
	var vpsID sql.NullString
	err := s.db.QueryRow(
		`SELECT id, incident_key, vps_id, severity, title, detail, status, created_at, updated_at, resolved_at FROM incidents WHERE id = ?`,
		id,
	).Scan(&inc.ID, &inc.IncidentKey, &vpsID, &inc.Severity, &inc.Title, &inc.Detail, &inc.Status, &inc.CreatedAt, &inc.UpdatedAt, &inc.ResolvedAt)
	if err != nil {
		return nil, err
	}
	if vpsID.Valid {
		v := vpsID.String
		inc.VPSID = &v
	}
	return &inc, nil
}

// GetSetting reads a global setting or returns defaultVal if absent.
func (s *Store) GetSetting(key, defaultVal string) string {
	var val string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val); err != nil {
		return defaultVal
	}
	return val
}

// SetSetting upserts a global key-value setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}
