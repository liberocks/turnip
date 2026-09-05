package turnip

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

func (c *Client) enqueue(message outboundMessage) bool {
	select {
	case <-c.done:
		return false
	case c.send <- message:
		return true
	default:
		c.hub.metrics.DroppedMessages.WithLabelValues("slow_consumer").Inc()
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(closeSlowClient, "slow consumer"), time.Now().Add(time.Second))
		_ = c.conn.Close()
		return false
	}
}

func (c *Client) readPump() {
	defer c.hub.unregister(c)
	c.conn.SetReadLimit(MaxMessageBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		messageType, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, closeDisbanded) {
				log.Debug().Err(err).Str("participant_id", c.id).Msg("WebSocket read ended")
			}
			return
		}
		if !c.limiter.Allow() {
			_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "rate limit exceeded"), time.Now().Add(time.Second))
			return
		}
		if messageType != websocket.TextMessage {
			c.sendError("invalid_message", "only text messages are supported", "")
			continue
		}
		envelope, err := DecodeClientMessage(data)
		if err != nil {
			c.hub.metrics.Messages.WithLabelValues("inbound", "invalid").Inc()
			c.sendError("invalid_message", "message does not match the v1 protocol", "")
			continue
		}
		c.hub.metrics.Messages.WithLabelValues("inbound", boundedMessageType(envelope.Type)).Inc()
		switch envelope.Type {
		case "signal":
			c.handleSignal(envelope)
		case "disband_room":
			c.handleDisband(envelope)
		default:
			c.sendError("unknown_type", "unsupported message type", envelope.ID)
		}
	}
}

func (c *Client) handleSignal(envelope ClientEnvelope) {
	signal, err := DecodeSignal(envelope.Data)
	if err != nil {
		c.sendError("invalid_signal", "signal data is invalid", envelope.ID)
		return
	}
	if signal.To == c.id {
		c.sendError("invalid_target", "cannot signal yourself", envelope.ID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, err = c.hub.store.Participant(ctx, c.identity.RoomID, signal.To)
	if err == nil {
		err = c.hub.store.PublishSignal(ctx, DistributedEvent{
			ID: randomID(), Type: "signal", RoomID: c.identity.RoomID, Origin: c.hub.instanceID,
			Target: signal.To, From: c.id, RequestID: envelope.ID, Payload: signal.Payload,
		})
	}
	cancel()
	if errors.Is(err, ErrNotParticipant) {
		c.sendError("participant_not_found", "target participant is not active in this room", envelope.ID)
		return
	}
	if err != nil {
		c.sendError("unavailable", "signaling service is temporarily unavailable", envelope.ID)
	}
}

func (c *Client) handleDisband(envelope ClientEnvelope) {
	if trimmed := bytes.TrimSpace(envelope.Data); len(trimmed) != 0 && !bytes.Equal(trimmed, []byte(`{}`)) {
		c.sendError("invalid_disband", "disband_room data must be an empty object", envelope.ID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := c.hub.store.Disband(ctx, c.identity.RoomID, c.identity.Subject, c.id, c.hub.instanceID, randomID())
	cancel()
	if errors.Is(err, ErrNotOwner) {
		c.sendError("forbidden", "only the room owner may disband it", envelope.ID)
		return
	}
	if err != nil {
		c.sendError("unavailable", "room could not be disbanded", envelope.ID)
	}
}

func (c *Client) sendError(code, message, requestID string) {
	c.enqueue(outboundMessage{
		typeName: "error",
		data:     EncodeMessage(ServerEnvelope{Type: "error", Data: ErrorData{Code: code, Message: message, RequestID: requestID}}),
	})
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer c.hub.unregister(c)
	for {
		select {
		case message := <-c.send:
			if len(message.data) > 0 {
				_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := c.conn.WriteMessage(websocket.TextMessage, message.data); err != nil {
					return
				}
				c.hub.metrics.Messages.WithLabelValues("outbound", boundedMessageType(message.typeName)).Inc()
			}
			if message.closeCode != 0 {
				_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(message.closeCode, message.closeText), time.Now().Add(writeWait))
				return
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := c.hub.store.Refresh(ctx, c.identity.RoomID, c.id)
			cancel()
			if err != nil {
				_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "presence refresh failed"), time.Now().Add(writeWait))
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

func boundedMessageType(value string) string {
	switch value {
	case "ready", "participant_joined", "participant_left", "signal", "disband_room", "room_disbanded", "error", "invalid":
		return value
	default:
		return "unknown"
	}
}

type rateLimiter struct {
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newRateLimiter(rate, burst int) rateLimiter {
	return rateLimiter{rate: float64(rate), burst: float64(burst), tokens: float64(burst), last: time.Now()}
}

func (l *rateLimiter) Allow() bool {
	now := time.Now()
	l.tokens += now.Sub(l.last).Seconds() * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
