package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

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
	Type      string            `json:"type"` // "discord", "telegram", "slack", "webhook"
	Config    map[string]string `json:"config"`
	Events    ChannelEvents     `json:"events"`
	Enabled   bool              `json:"enabled"`
	CreatedAt int64             `json:"created_at"`
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
	return out, nil
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

// UpdateChannel modifies an existing notification channel.
func (s *Store) UpdateChannel(id string, ch NotificationChannel) (*NotificationChannel, error) {
	cur, err := s.GetChannel(id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ch.Name) != "" {
		cur.Name = strings.TrimSpace(ch.Name)
	}
	if strings.TrimSpace(ch.Type) != "" {
		cur.Type = strings.ToLower(strings.TrimSpace(ch.Type))
	}
	if ch.Config != nil {
		cur.Config = ch.Config
	}
	cur.Events = ch.Events
	cur.Enabled = ch.Enabled
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
	return out, nil
}

// CreateOrUpdateIncident creates an open incident if none exists for incidentKey, returning (inc, isNew, error).
func (s *Store) CreateOrUpdateIncident(incidentKey, severity string, vpsID *string, title, detail string) (*Incident, bool, error) {
	now := time.Now().Unix()
	var existingID string
	err := s.db.QueryRow(
		`SELECT id FROM incidents WHERE incident_key = ? AND status IN ('open', 'acknowledged') ORDER BY created_at DESC LIMIT 1`,
		incidentKey,
	).Scan(&existingID)
	if err == nil {
		_, err = s.db.Exec(
			`UPDATE incidents SET severity=?, title=?, detail=?, updated_at=? WHERE id=?`,
			severity, title, detail, now, existingID,
		)
		if err != nil {
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
	_, err = s.db.Exec(
		`INSERT INTO incidents (id, incident_key, vps_id, severity, title, detail, status, created_at, updated_at, resolved_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?, 0)`,
		id, incidentKey, vpsArg, severity, title, detail, now, now,
	)
	if err != nil {
		return nil, false, err
	}
	inc, err := s.getIncident(id)
	return inc, true, err
}

// ResolveIncidentByKey marks any open/acknowledged incident for incidentKey as resolved.
func (s *Store) ResolveIncidentByKey(incidentKey, detail string) (*Incident, bool, error) {
	var existingID string
	err := s.db.QueryRow(
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
	_, err = s.db.Exec(
		`UPDATE incidents SET status = 'resolved', detail = ?, updated_at = ?, resolved_at = ? WHERE id = ?`,
		detail, now, now, existingID,
	)
	if err != nil {
		return nil, false, err
	}
	inc, err := s.getIncident(existingID)
	return inc, true, err
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
