package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Hub maintains distributed state across multiple server instances
type Hub struct {
	// Local clients connected to this instance
	LocalClients map[*Client]bool

	// Register requests from local clients
	Register chan *Client

	// Unregister requests from local clients
	Unregister chan *Client

	// Inbound messages from local clients to broadcast
	Broadcast chan BroadcastMessage

	// Redis client for pub/sub and state management
	RedisClient *redis.Client

	// Pub/Sub subscription
	PubSub *redis.PubSub

	// Instance ID to identify this server instance
	InstanceID string

	// Mutex for thread-safe operations on local clients
	mutex sync.RWMutex

	// Context for graceful shutdown
	ctx    context.Context
	cancel context.CancelFunc
}

// RedisMessage represents a message sent through Redis pub/sub
type RedisMessage struct {
	Type       string      `json:"type"`
	Room       string      `json:"room"`
	Data       interface{} `json:"data"`
	Username   string      `json:"username"`
	InstanceID string      `json:"instance_id"`
	Timestamp  time.Time   `json:"timestamp"`
}

// ClientInfo represents client information stored in Redis
type ClientInfo struct {
	Username    string    `json:"username"`
	UserID      string    `json:"user_id"`
	Email       string    `json:"email"`
	Room        string    `json:"room"`
	InstanceID  string    `json:"instance_id"`
	ConnectedAt time.Time `json:"connected_at"`
}

// NewHub creates a new Redis-based Hub
func NewHub(redisClient *redis.Client, instanceID string) *Hub {
	ctx, cancel := context.WithCancel(context.Background())

	hub := &Hub{
		LocalClients: make(map[*Client]bool),
		Register:     make(chan *Client),
		Unregister:   make(chan *Client),
		Broadcast:    make(chan BroadcastMessage),
		RedisClient:  redisClient,
		InstanceID:   instanceID,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Subscribe to Redis pub/sub channels
	hub.PubSub = redisClient.PSubscribe(ctx, "signaling:broadcast", "signaling:room:*")

	return hub
}

// getRedisResult converts an error to a result string for metrics
func getRedisResult(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

// Run starts the Redis hub
func (h *Hub) Run() {
	// Start Redis pub/sub message handler
	go h.handleRedisPubSub()

	// Start cleanup routine for expired clients
	go h.cleanupExpiredClients()

	for {
		select {
		case client := <-h.Register:
			h.registerClient(client)

		case client := <-h.Unregister:
			h.unregisterClient(client)

		case message := <-h.Broadcast:
			h.broadcastMessage(message)

		case <-h.ctx.Done():
			log.Info().Msg("Redis hub shutting down")
			return
		}
	}
}

// registerClient registers a client both locally and in Redis
func (h *Hub) registerClient(client *Client) {
	h.mutex.Lock()
	h.LocalClients[client] = true
	h.mutex.Unlock()

	// Store client info in Redis with expiration
	clientInfo := ClientInfo{
		Username:    client.Username,
		UserID:      client.UserID,
		Email:       client.Email,
		Room:        client.Room,
		InstanceID:  h.InstanceID,
		ConnectedAt: time.Now(),
	}

	clientKey := fmt.Sprintf("client:%s:%s", client.Room, client.UserID)
	clientData, _ := json.Marshal(clientInfo)

	start := time.Now()
	err := h.RedisClient.Set(h.ctx, clientKey, clientData, (7 * time.Hour)).Err()
	RecordRedisOperation("set", getRedisResult(err), time.Since(start))
	if err != nil {
		log.Error().Err(err).Msg("Failed to store client info in Redis")
		RecordRedisError("set", "store_client_info")
	}

	// Add client to room set
	roomKey := fmt.Sprintf("room:%s", client.Room)
	start = time.Now()
	err = h.RedisClient.SAdd(h.ctx, roomKey, client.UserID).Err()
	RecordRedisOperation("sadd", getRedisResult(err), time.Since(start))
	if err != nil {
		log.Error().Err(err).Msg("Failed to add client to room in Redis")
		RecordRedisError("sadd", "add_to_room")
	}

	start = time.Now()
	h.RedisClient.Expire(h.ctx, roomKey, (7 * time.Hour))
	RecordRedisOperation("expire", "success", time.Since(start))

	log.Info().
		Str("userID", client.UserID).
		Str("room", client.Room).
		Str("instance", h.InstanceID).
		Msg("Client registered")

	// Publish join event to other instances
	joinMessage := RedisMessage{
		Type:       "user_joined",
		Room:       client.Room,
		Username:   client.Username,
		InstanceID: h.InstanceID,
		Timestamp:  time.Now(),
		Data: map[string]string{
			"username": client.Username,
			"user_id":  client.UserID,
			"email":    client.Email,
		},
	}

	h.publishToRedis("signaling:broadcast", joinMessage)
	h.publishToRedis(fmt.Sprintf("signaling:room:%s", client.Room), joinMessage)

	// Send ready message to local clients in the same room
	h.broadcastToLocalClients(client.Room, BroadcastMessage{
		Room: client.Room,
		Data: map[string]string{
			"username": client.Username,
			"user_id":  client.UserID,
			"email":    client.Email,
		},
		Type:   "ready",
		Sender: client,
	})
}

// unregisterClient unregisters a client both locally and from Redis
func (h *Hub) unregisterClient(client *Client) {
	h.mutex.Lock()
	if _, ok := h.LocalClients[client]; ok {
		delete(h.LocalClients, client)
		close(client.Send)
	}
	h.mutex.Unlock()

	if client.Room != "" && client.UserID != "" {
		// Record disconnection
		RecordDisconnection(Conf.Realm)
		RecordRoomLeave(Conf.Realm)

		// Remove client info from Redis
		clientKey := fmt.Sprintf("client:%s:%s", client.Room, client.UserID)
		start := time.Now()
		err := h.RedisClient.Del(h.ctx, clientKey).Err()
		RecordRedisOperation("del", getRedisResult(err), time.Since(start))
		if err != nil {
			RecordRedisError("del", "remove_client_info")
		}

		// Check if room will become empty after removing this client
		roomKey := fmt.Sprintf("room:%s", client.Room)
		remainingMembers, _ := h.RedisClient.SMembers(h.ctx, roomKey).Result()

		// Remove client from room set
		start = time.Now()
		err = h.RedisClient.SRem(h.ctx, roomKey, client.UserID).Err()
		RecordRedisOperation("srem", getRedisResult(err), time.Since(start))
		if err != nil {
			RecordRedisError("srem", "remove_from_room")
		}

		// Check if room is now empty and send disbandment event
		if len(remainingMembers) == 1 && len(remainingMembers) > 0 && remainingMembers[0] == client.UserID {
			// Room will be empty after this client leaves
			h.sendRoomDisbandedEvent(client.Room)
		}

		log.Info().
			Str("username", client.Username).
			Str("room", client.Room).
			Str("instance", h.InstanceID).
			Msg("Client unregistered")

		// Publish leave event to other instances
		leaveMessage := RedisMessage{
			Type:       "user_left",
			Room:       client.Room,
			Username:   client.Username,
			InstanceID: h.InstanceID,
			Timestamp:  time.Now(),
			Data: map[string]string{
				"username": client.Username,
				"user_id":  client.UserID,
				"email":    client.Email,
			},
		}

		h.publishToRedis("signaling:broadcast", leaveMessage)
		h.publishToRedis(fmt.Sprintf("signaling:room:%s", client.Room), leaveMessage)
	}
}

// sendRoomDisbandedEvent sends a room_disbanded event to all members before the room is removed
func (h *Hub) sendRoomDisbandedEvent(room string) {
	log.Info().
		Str("room", room).
		Str("instance", h.InstanceID).
		Msg("Sending room disbanded event")

	// Create room disbanded message
	disbandMessage := RedisMessage{
		Type:       "room_disbanded",
		Room:       room,
		Username:   "",
		InstanceID: h.InstanceID,
		Timestamp:  time.Now(),
		Data: map[string]string{
			"room":   room,
			"reason": "last_member_left",
		},
	}

	// Broadcast to all instances listening to this room
	h.publishToRedis("signaling:broadcast", disbandMessage)
	h.publishToRedis(fmt.Sprintf("signaling:room:%s", room), disbandMessage)

	// Also broadcast to local clients in the room
	h.broadcastToLocalClients(room, BroadcastMessage{
		Room: room,
		Data: map[string]string{
			"room":   room,
			"reason": "last_member_left",
		},
		Type:   "room_disbanded",
		Sender: nil,
	})
}

// broadcastMessage sends a message to all clients in a room across all instances
func (h *Hub) broadcastMessage(message BroadcastMessage) {
	log.Info().
		Str("room", message.Room).
		Str("type", message.Type).
		Bool("has_sender", message.Sender != nil).
		Msg("Broadcasting message")

	// First broadcast to local clients
	h.broadcastToLocalClients(message.Room, message)

	// Then publish to Redis for other instances
	username := ""
	if message.Sender != nil {
		username = message.Sender.Username
	}

	redisMessage := RedisMessage{
		Type:       message.Type,
		Room:       message.Room,
		Data:       message.Data,
		Username:   username,
		InstanceID: h.InstanceID,
		Timestamp:  time.Now(),
	}

	channelName := fmt.Sprintf("signaling:room:%s", message.Room)
	log.Debug().
		Str("channel", channelName).
		Str("instance", h.InstanceID).
		Msg("Publishing to Redis channel")

	h.publishToRedis(channelName, redisMessage)
}

// broadcastToLocalClients sends a message to local clients in a room
func (h *Hub) broadcastToLocalClients(room string, message BroadcastMessage) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	clientCount := 0
	for client := range h.LocalClients {
		if client.Room == room {
			clientCount++
		}
	}

	log.Debug().
		Str("room", room).
		Str("message_type", message.Type).
		Int("local_clients_in_room", clientCount).
		Bool("has_sender", message.Sender != nil).
		Msg("Broadcasting to local clients")

	for client := range h.LocalClients {
		if client.Room != room {
			continue
		}

		// Skip sender for local broadcasts
		if client == message.Sender {
			log.Debug().
				Str("userID", client.UserID).
				Str("room", room).
				Msg("Skipping message sender")
			continue
		}

		log.Debug().
			Str("userID", client.UserID).
			Str("room", room).
			Str("message_type", message.Type).
			Msg("Sending message to client")

		select {
		case client.Send <- h.encodeMessage(message):
		default:
			// Client's send channel is full, close it
			log.Warn().
				Str("userID", client.UserID).
				Str("room", room).
				Msg("Client send channel full, removing client")
			close(client.Send)
			delete(h.LocalClients, client)
		}
	}
}

// publishToRedis publishes a message to a Redis channel
func (h *Hub) publishToRedis(channel string, message RedisMessage) {
	data, err := json.Marshal(message)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal Redis message")
		RecordRedisError("publish", "marshal_error")
		return
	}

	start := time.Now()
	err = h.RedisClient.Publish(h.ctx, channel, data).Err()
	RecordRedisOperation("publish", getRedisResult(err), time.Since(start))
	if err != nil {
		log.Error().Err(err).Str("channel", channel).Msg("Failed to publish to Redis")
		RecordRedisError("publish", "publish_error")
	}
}

// handleRedisPubSub handles incoming Redis pub/sub messages
func (h *Hub) handleRedisPubSub() {
	log.Info().Msg("Starting Redis pub/sub message handler")

	for {
		select {
		case msg := <-h.PubSub.Channel():
			log.Debug().
				Str("channel", msg.Channel).
				Str("payload", msg.Payload).
				Msg("Received Redis message")

			var redisMessage RedisMessage
			err := json.Unmarshal([]byte(msg.Payload), &redisMessage)
			if err != nil {
				log.Error().Err(err).Msg("Failed to unmarshal Redis message")
				continue
			}

			// Skip messages from this instance
			if redisMessage.InstanceID == h.InstanceID {
				log.Debug().
					Str("message_instance", redisMessage.InstanceID).
					Str("this_instance", h.InstanceID).
					Msg("Skipping message from same instance")
				continue
			}

			log.Info().
				Str("type", redisMessage.Type).
				Str("room", redisMessage.Room).
				Str("from_instance", redisMessage.InstanceID).
				Str("username", redisMessage.Username).
				Msg("Processing message from another instance")

			// Convert Redis message to broadcast message and send to local clients
			broadcastMsg := BroadcastMessage{
				Room: redisMessage.Room,
				Data: redisMessage.Data,
				Type: redisMessage.Type,
				// No sender since it's from another instance
				Sender: nil,
			}

			h.broadcastToLocalClients(redisMessage.Room, broadcastMsg)

		case <-h.ctx.Done():
			log.Info().Msg("Redis pub/sub handler shutting down")
			return
		}
	}
}

// cleanupExpiredClients removes expired client information from Redis
func (h *Hub) cleanupExpiredClients() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Keep local clients alive in Redis
			h.mutex.RLock()
			for client := range h.LocalClients {
				if client.Room != "" && client.UserID != "" {
					clientKey := fmt.Sprintf("client:%s:%s", client.Room, client.UserID)
					h.RedisClient.Expire(h.ctx, clientKey, (7 * time.Hour))

					roomKey := fmt.Sprintf("room:%s", client.Room)
					h.RedisClient.Expire(h.ctx, roomKey, (7 * time.Hour))
				}
			}
			h.mutex.RUnlock()

		case <-h.ctx.Done():
			return
		}
	}
}

// encodeMessage encodes a broadcast message to JSON
func (h *Hub) encodeMessage(msg BroadcastMessage) []byte {
	response := map[string]interface{}{
		"type": msg.Type,
		"data": msg.Data,
	}

	data, err := json.Marshal(response)
	if err != nil {
		log.Error().Err(err).Msg("Failed to encode message")
		return []byte("{}")
	}
	return data
}

// GetRoomMembers returns a list of all user IDs in a room across all instances
func (h *Hub) GetRoomMembers(room string) ([]string, error) {
	roomKey := fmt.Sprintf("room:%s", room)
	members, err := h.RedisClient.SMembers(h.ctx, roomKey).Result()
	if err != nil {
		return nil, err
	}
	return members, nil
}

// IsUserMemberOfRoom checks if a user is a member of a specific room by verifying their client key exists
func (h *Hub) IsUserMemberOfRoom(room, userID string) (bool, error) {
	clientKey := fmt.Sprintf("client:%s:%s", room, userID)
	exists, err := h.RedisClient.Exists(h.ctx, clientKey).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

// DisbandRoom allows manual room disbandment with a custom reason
func (h *Hub) DisbandRoom(room string, reason string) {
	log.Info().
		Str("room", room).
		Str("reason", reason).
		Str("instance", h.InstanceID).
		Msg("Manually disbanding room")

	// Create room disbanded message with custom reason
	disbandMessage := RedisMessage{
		Type:       "room_disbanded",
		Room:       room,
		Username:   "",
		InstanceID: h.InstanceID,
		Timestamp:  time.Now(),
		Data: map[string]string{
			"room":   room,
			"reason": reason,
		},
	}

	// Broadcast to all instances listening to this room
	h.publishToRedis("signaling:broadcast", disbandMessage)
	h.publishToRedis(fmt.Sprintf("signaling:room:%s", room), disbandMessage)

	// Also broadcast to local clients in the room
	h.broadcastToLocalClients(room, BroadcastMessage{
		Room: room,
		Data: map[string]string{
			"room":   room,
			"reason": reason,
		},
		Type:   "room_disbanded",
		Sender: nil,
	})

	// Force remove all clients from the room
	roomKey := fmt.Sprintf("room:%s", room)
	members, err := h.RedisClient.SMembers(h.ctx, roomKey).Result()
	if err == nil {
		// Remove each member and their client info
		for _, member := range members {
			clientKey := fmt.Sprintf("client:%s:%s", room, member)
			h.RedisClient.Del(h.ctx, clientKey)
		}
		// Remove the room entirely
		h.RedisClient.Del(h.ctx, roomKey)
	}

	// Disconnect all local clients in this room
	h.mutex.Lock()
	defer h.mutex.Unlock()

	clientsToRemove := make([]*Client, 0)
	for client := range h.LocalClients {
		if client.Room == room {
			clientsToRemove = append(clientsToRemove, client)
		}
	}

	for _, client := range clientsToRemove {
		if _, ok := h.LocalClients[client]; ok {
			delete(h.LocalClients, client)
			close(client.Send)
			// Close the WebSocket connection gracefully
			if err := client.Conn.Close(); err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error().Err(err).
					Str("userID", client.UserID).
					Str("room", client.Room).
					Msg("Failed to close WebSocket connection during room disbandment")
			}
		}
	}
}

// Shutdown gracefully shuts down the Redis hub
func (h *Hub) Shutdown() {
	log.Info().Msg("Shutting down Redis hub")
	h.cancel()

	if h.PubSub != nil {
		if err := h.PubSub.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close Redis PubSub connection")
		}
	}
}
