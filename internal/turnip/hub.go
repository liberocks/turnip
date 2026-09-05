package turnip

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

const (
	writeWait       = 10 * time.Second
	pongWait        = 70 * time.Second
	pingPeriod      = 30 * time.Second
	closeDisbanded  = 4001
	closeSlowClient = 1013
)

type outboundMessage struct {
	data      []byte
	typeName  string
	closeCode int
	closeText string
}

type Client struct {
	id       string
	identity Identity
	owner    bool
	conn     *websocket.Conn
	hub      *Hub
	send     chan outboundMessage
	done     chan struct{}
	once     sync.Once
	limiter  rateLimiter
}

type Hub struct {
	store       RoomStore
	auth        *Authenticator
	metrics     *Metrics
	instanceID  string
	origins     map[string]struct{}
	allowAny    bool
	clientsMu   sync.RWMutex
	clients     map[string]*Client
	ready       atomic.Bool
	subCancel   context.CancelFunc
	workers     sync.WaitGroup
	connections sync.WaitGroup
}

func NewHub(store RoomStore, auth *Authenticator, metrics *Metrics, instanceID string, allowedOrigins []string) *Hub {
	origins := make(map[string]struct{}, len(allowedOrigins))
	allowAny := false
	for _, origin := range allowedOrigins {
		if origin == "*" {
			allowAny = true
			continue
		}
		origins[strings.ToLower(origin)] = struct{}{}
	}
	return &Hub{
		store: store, auth: auth, metrics: metrics, instanceID: instanceID,
		origins: origins, allowAny: allowAny, clients: make(map[string]*Client),
	}
}

func (h *Hub) Start(ctx context.Context) error {
	subCtx, cancel := context.WithCancel(ctx)
	subscription, err := h.store.Subscribe(subCtx)
	if err != nil {
		cancel()
		return err
	}
	h.subCancel = cancel
	h.ready.Store(true)
	h.workers.Add(2)
	go func() {
		defer h.workers.Done()
		defer func() { _ = subscription.Close() }()
		channel := subscription.Channel()
		for {
			select {
			case message, ok := <-channel:
				if !ok {
					h.ready.Store(false)
					return
				}
				var event DistributedEvent
				if err := json.Unmarshal([]byte(message.Payload), &event); err != nil {
					log.Warn().Err(err).Msg("discarding invalid Redis event")
					continue
				}
				h.handleEvent(event)
			case <-subCtx.Done():
				h.ready.Store(false)
				return
			}
		}
	}()
	go func() {
		defer h.workers.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sweepCtx, sweepCancel := context.WithTimeout(subCtx, 5*time.Second)
				err := h.store.Sweep(sweepCtx, h.instanceID)
				sweepCancel()
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Warn().Err(err).Msg("presence sweep failed")
				}
			case <-subCtx.Done():
				return
			}
		}
	}()
	return nil
}

func (h *Hub) Ready() bool { return h.ready.Load() }

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	identity, err := h.auth.AuthenticateRequest(r)
	if err != nil {
		h.metrics.AuthAttempts.WithLabelValues("failure", "invalid_token").Inc()
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	h.metrics.AuthAttempts.WithLabelValues("success", "valid").Inc()

	upgrader := websocket.Upgrader{
		HandshakeTimeout: writeWait,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		CheckOrigin:      h.checkOrigin,
	}
	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &Client{
		id: randomID(), identity: identity, conn: connection, hub: h,
		send: make(chan outboundMessage, 256), done: make(chan struct{}),
		limiter: newRateLimiter(MessagesPerSecond, MessageBurst),
	}
	h.addClient(client)
	joinCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	joinResult, err := h.store.Join(joinCtx, identity, client.id, h.instanceID, randomID())
	cancel()
	if err != nil {
		h.removeLocal(client)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = h.store.Leave(cleanupCtx, identity.RoomID, client.id, h.instanceID, randomID())
		cleanupCancel()
		code := websocket.CloseTryAgainLater
		message := "room unavailable"
		if errors.Is(err, ErrRoomFull) {
			code = websocket.ClosePolicyViolation
			message = "room is full"
		}
		_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, message), time.Now().Add(writeWait))
		_ = connection.Close()
		return
	}
	client.owner = joinResult.Owner
	h.connections.Add(1)
	h.metrics.ConnectionsActive.Inc()
	h.metrics.ConnectionsTotal.Inc()
	h.metrics.RoomEvents.WithLabelValues("join").Inc()
	client.enqueue(outboundMessage{
		typeName: "ready",
		data: EncodeMessage(ServerEnvelope{Type: "ready", Data: ReadyData{
			RoomID: identity.RoomID, ParticipantID: client.id, Owner: client.owner, Participants: joinResult.Participants,
		}}),
	})
	go client.writePump()
	go client.readPump()
}

func (h *Hub) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if h.allowAny {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	normalized := strings.ToLower(parsed.Scheme + "://" + parsed.Host)
	_, ok := h.origins[normalized]
	return ok
}

func (h *Hub) addClient(client *Client) {
	h.clientsMu.Lock()
	h.clients[client.id] = client
	h.clientsMu.Unlock()
}

func (h *Hub) removeLocal(client *Client) {
	h.clientsMu.Lock()
	if current, ok := h.clients[client.id]; ok && current == client {
		delete(h.clients, client.id)
	}
	h.clientsMu.Unlock()
}

func (h *Hub) unregister(client *Client) {
	client.once.Do(func() {
		close(client.done)
		h.removeLocal(client)
		_ = client.conn.Close()
		leaveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := h.store.Leave(leaveCtx, client.identity.RoomID, client.id, h.instanceID, randomID())
		cancel()
		if err != nil {
			log.Warn().Err(err).Str("participant_id", client.id).Msg("failed to remove participant")
		}
		h.metrics.ConnectionsActive.Dec()
		h.metrics.RoomEvents.WithLabelValues("leave").Inc()
		h.connections.Done()
	})
}

func (h *Hub) handleEvent(event DistributedEvent) {
	switch event.Type {
	case "participant_joined":
		if event.Participant == nil {
			return
		}
		h.broadcastRoom(event.RoomID, event.Participant.ID, outboundMessage{
			typeName: "participant_joined",
			data:     EncodeMessage(ServerEnvelope{Type: "participant_joined", Data: event.Participant}),
		})
	case "participant_left":
		if event.Participant == nil {
			return
		}
		h.broadcastRoom(event.RoomID, event.Participant.ID, outboundMessage{
			typeName: "participant_left",
			data:     EncodeMessage(ServerEnvelope{Type: "participant_left", Data: event.Participant}),
		})
	case "signal":
		h.clientsMu.RLock()
		client := h.clients[event.Target]
		h.clientsMu.RUnlock()
		if client != nil && client.identity.RoomID == event.RoomID {
			client.enqueue(outboundMessage{
				typeName: "signal",
				data: EncodeMessage(ServerEnvelope{Type: "signal", Data: SignalData{
					From: event.From, Payload: event.Payload, RequestID: event.RequestID,
				}}),
			})
		}
	case "room_disbanded":
		h.metrics.RoomEvents.WithLabelValues("disband").Inc()
		message := outboundMessage{
			typeName: "room_disbanded", closeCode: closeDisbanded, closeText: "room disbanded",
			data: EncodeMessage(ServerEnvelope{Type: "room_disbanded", Data: map[string]string{
				"reason": event.Reason, "by": event.By,
			}}),
		}
		h.broadcastRoom(event.RoomID, "", message)
	}
}

func (h *Hub) broadcastRoom(roomID, excludeID string, message outboundMessage) {
	h.clientsMu.RLock()
	clients := make([]*Client, 0)
	for _, client := range h.clients {
		if client.identity.RoomID == roomID && client.id != excludeID {
			clients = append(clients, client)
		}
	}
	h.clientsMu.RUnlock()
	for _, client := range clients {
		client.enqueue(message)
	}
}

func (h *Hub) Shutdown(ctx context.Context) {
	h.ready.Store(false)
	if h.subCancel != nil {
		h.subCancel()
	}
	h.clientsMu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for _, client := range h.clients {
		clients = append(clients, client)
	}
	h.clientsMu.RUnlock()
	for _, client := range clients {
		client.enqueue(outboundMessage{closeCode: websocket.CloseGoingAway, closeText: "server shutting down"})
	}
	done := make(chan struct{})
	go func() {
		h.connections.Wait()
		h.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		for _, client := range clients {
			_ = client.conn.Close()
		}
	}
}

func randomID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(data)
}
