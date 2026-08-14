package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/visoraft/visoraft/internal/identity"
)

const SpecVersion = "1.0"

type Envelope struct {
	SpecVersion string          `json:"specversion"`
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Source      string          `json:"source"`
	Subject     string          `json:"subject"`
	Time        time.Time       `json:"time"`
	Data        json.RawMessage `json:"data"`
}

func New(eventType, source, subject string, occurredAt time.Time, data any) (Envelope, error) {
	messageID, err := identity.NewUUID()
	if err != nil {
		return Envelope{}, err
	}
	rawData, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal event data: %w", err)
	}

	return Envelope{
		SpecVersion: SpecVersion,
		ID:          messageID,
		Type:        eventType,
		Source:      source,
		Subject:     subject,
		Time:        occurredAt.UTC(),
		Data:        rawData,
	}, nil
}

func Decode(raw []byte) (Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("decode event envelope: %w", err)
	}
	if envelope.SpecVersion != SpecVersion || envelope.ID == "" || envelope.Type == "" || envelope.Source == "" {
		return Envelope{}, fmt.Errorf("invalid event envelope")
	}
	return envelope, nil
}
