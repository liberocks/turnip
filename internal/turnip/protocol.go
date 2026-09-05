package turnip

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

type ClientEnvelope struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type SignalRequest struct {
	To      string          `json:"to"`
	Payload json.RawMessage `json:"payload"`
}

type ServerEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type ErrorData struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

type Participant struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Owner bool   `json:"owner"`
}

type ReadyData struct {
	RoomID        string        `json:"room_id"`
	ParticipantID string        `json:"participant_id"`
	Owner         bool          `json:"owner"`
	Participants  []Participant `json:"participants"`
}

type SignalData struct {
	From      string          `json:"from"`
	Payload   json.RawMessage `json:"payload"`
	RequestID string          `json:"request_id,omitempty"`
}

func DecodeClientMessage(data []byte) (ClientEnvelope, error) {
	var envelope ClientEnvelope
	if err := decodeStrict(data, &envelope); err != nil {
		return ClientEnvelope{}, err
	}
	if !requestIDPattern.MatchString(envelope.ID) {
		return ClientEnvelope{}, errors.New("id must contain 1-64 safe characters")
	}
	if envelope.Type == "" {
		return ClientEnvelope{}, errors.New("type is required")
	}
	return envelope, nil
}

func DecodeSignal(data json.RawMessage) (SignalRequest, error) {
	var signal SignalRequest
	if err := decodeStrict(data, &signal); err != nil {
		return SignalRequest{}, err
	}
	if !requestIDPattern.MatchString(signal.To) {
		return SignalRequest{}, errors.New("to is invalid")
	}
	trimmed := bytes.TrimSpace(signal.Payload)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || !json.Valid(trimmed) {
		return SignalRequest{}, errors.New("payload must be valid non-null JSON")
	}
	return signal, nil
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("message must contain exactly one JSON value")
	}
	return nil
}

func EncodeMessage(message ServerEnvelope) []byte {
	data, err := json.Marshal(message)
	if err != nil {
		return []byte(`{"type":"error","data":{"code":"internal_error","message":"internal server error"}}`)
	}
	return data
}
