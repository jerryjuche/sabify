package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type EventRecord struct {
	EventID     string
	EventType   string
	Payload     []byte
	ProcessedAt time.Time
}

type WebhookEventModel struct {
	DB *pgxpool.Pool
}

// InsertIgnore records an event idempotently. returns true if the event was new.
func (m *WebhookEventModel) InsertIgnore(ctx context.Context, eventID, eventType string, payload []byte) (bool, error) {
	query := `
		INSERT INTO webhook_events (event_id, event_type, payload)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id) DO NOTHING
	`

	result, err := m.DB.Exec(ctx, query, eventID, eventType, payload)
	if err != nil {
		return false, err
	}

	return result.RowsAffected() > 0, nil
}
