package turnip

import (
	"encoding/json"
	"testing"
)

func TestDecodeClientMessage(t *testing.T) {
	valid := []byte(`{"id":"request-1","type":"signal","data":{"to":"participant_2","payload":{"type":"offer"}}}`)
	envelope, err := DecodeClientMessage(valid)
	if err != nil {
		t.Fatal(err)
	}
	signal, err := DecodeSignal(envelope.Data)
	if err != nil || signal.To != "participant_2" || !json.Valid(signal.Payload) {
		t.Fatalf("unexpected signal: %+v, %v", signal, err)
	}

	invalid := [][]byte{
		[]byte(`{"type":"signal"}`),
		[]byte(`{"id":"x","type":"signal","unknown":true}`),
		[]byte(`{"id":"x","type":"signal"} trailing`),
		[]byte(`{"id":"bad id","type":"signal"}`),
	}
	for _, data := range invalid {
		if _, err := DecodeClientMessage(data); err == nil {
			t.Fatalf("expected invalid message %s to fail", data)
		}
	}
}

func TestDecodeSignalRejectsInvalidPayloadAndTarget(t *testing.T) {
	for _, data := range []string{
		`{"to":"bad target","payload":{}}`,
		`{"to":"target","payload":null}`,
		`{"to":"target"}`,
		`{"to":"target","payload":{},"extra":true}`,
	} {
		if _, err := DecodeSignal(json.RawMessage(data)); err == nil {
			t.Fatalf("expected invalid signal %s to fail", data)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	limiter := newRateLimiter(1, 2)
	first, second, third := limiter.Allow(), limiter.Allow(), limiter.Allow()
	if !first || !second || third {
		t.Fatal("burst limit was not enforced")
	}
}
