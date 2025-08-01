package main

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// Client represents a WebSocket client
type Client struct {
	Username string
	UserID   string
	Email    string
	Room     string
	Conn     *websocket.Conn
	Send     chan []byte
	Hub      *Hub
}

// BroadcastMessage represents a message to be broadcast
type BroadcastMessage struct {
	Room   string      `json:"room"`
	Data   interface{} `json:"data"`
	Sender *Client     `json:"-"`
	Type   string      `json:"type"`
}

// WebSocket message types
type JoinMessage struct {
	Username string `json:"username"`
	Room     string `json:"room"`
}

type DataMessage struct {
	Username string      `json:"username"`
	UserID   string      `json:"user_id"`
	Room     string      `json:"room"`
	Data     interface{} `json:"data"`
}

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow connections from any origin
		return true
	},
}

// readPump pumps messages from the websocket connection to the hub
func (c *Client) readPump() {
	defer func() {
		c.Hub.Unregister <- c
		// Close connection only if it's not already closed
		if err := c.Conn.Close(); err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
			log.Error().Err(err).Msg("Failed to close WebSocket connection in readPump")
		}
	}()

	for {
		var message map[string]interface{}
		err := c.Conn.ReadJSON(&message)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error().Err(err).Msg("WebSocket error")
			}
			break
		}

		// Record message received
		RecordMessageReceived(Conf.Realm, "unknown")

		// Handle different message types
		if msgType, ok := message["type"].(string); ok {
			// Record message type specific metrics
			RecordMessageReceived(Conf.Realm, msgType)

			switch msgType {
			case "join":
				// Handle join message
				if userID, ok := message["user_id"].(string); ok {
					if room, ok := message["room"].(string); ok {
						c.UserID = userID
						c.Username = userID // Use userID as username
						c.Room = room
						c.Hub.Register <- c
						RecordRoomJoin(Conf.Realm)
						log.Info().
							Str("userID", userID).
							Str("room", room).
							Msg("Client requested to join room")
					}
				}

			case "data":
				// Handle data message
				if c.Room == "" || c.UserID == "" {
					log.Warn().
						Str("userID", c.UserID).
						Str("room", c.Room).
						Msg("Client attempted to send data without being in a room")
					// Send error message back to client
					errorMsg := map[string]interface{}{
						"type":  "error",
						"error": "You must join a room before sending data",
					}
					if errorData, err := json.Marshal(errorMsg); err == nil {
						select {
						case c.Send <- errorData:
						default:
							// Channel is full, skip
						}
					}
					continue
				}

				if data, ok := message["data"]; ok {
					log.Info().
						Str("userID", c.UserID).
						Str("room", c.Room).
						Interface("data", data).
						Msg("Client sent data")

					// Determine signaling message type
					signalType := "unknown"
					if dataMap, ok := data.(map[string]interface{}); ok {
						if msgType, exists := dataMap["type"].(string); exists {
							signalType = msgType
						}
					}
					RecordSignalingMessage(Conf.Realm, signalType)

					c.Hub.Broadcast <- BroadcastMessage{
						Room:   c.Room,
						Data:   data,
						Type:   "data",
						Sender: c,
					}
				}

			case "disband_room":
				// Handle room disbandment request
				if c.Room == "" || c.UserID == "" {
					log.Warn().
						Str("userID", c.UserID).
						Str("room", c.Room).
						Msg("Client attempted to disband room without being in a room")
					// Send error message back to client
					errorMsg := map[string]interface{}{
						"type":  "error",
						"error": "You must be a member of a room to disband it",
					}
					if errorData, err := json.Marshal(errorMsg); err == nil {
						select {
						case c.Send <- errorData:
						default:
							// Channel is full, skip
						}
					}
					continue
				}

				// Verify the client is actually a member of the room by checking if their client key exists
				isMember, err := c.Hub.IsUserMemberOfRoom(c.Room, c.UserID)
				if err != nil {
					log.Error().Err(err).
						Str("userID", c.UserID).
						Str("room", c.Room).
						Msg("Failed to verify room membership for disbandment")
					// Send error message back to client
					errorMsg := map[string]interface{}{
						"type":  "error",
						"error": "Failed to verify room membership",
					}
					if errorData, err := json.Marshal(errorMsg); err == nil {
						select {
						case c.Send <- errorData:
						default:
							// Channel is full, skip
						}
					}
					continue
				}

				if !isMember {
					log.Warn().
						Str("userID", c.UserID).
						Str("room", c.Room).
						Str("username", c.Username).
						Msg("Client attempted to disband room they are not a member of")
					// Send error message back to client
					errorMsg := map[string]interface{}{
						"type":  "error",
						"error": "You are not a member of this room",
					}
					if errorData, err := json.Marshal(errorMsg); err == nil {
						select {
						case c.Send <- errorData:
						default:
							// Channel is full, skip
						}
					}
					continue
				}

				log.Info().
					Str("userID", c.UserID).
					Str("room", c.Room).
					Str("username", c.Username).
					Msg("Client requested room disbandment")

				// Send room disbanded event to all clients in the room
				c.Hub.DisbandRoom(c.Room, "user_requested")
			}
		}
	}
}

// writePump pumps messages from the hub to the websocket connection
func (c *Client) writePump() {
	defer func() {
		// Close connection only if it's not already closed
		if err := c.Conn.Close(); err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
			log.Error().Err(err).Msg("Failed to close WebSocket connection in writePump")
		}
	}()

	for message := range c.Send {
		if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
			log.Error().Err(err).Msg("Failed to write message")
			return
		}
		// Record message sent
		RecordMessageSent(Conf.Realm, "outbound")
	}
}

// WebSocketHandler handles WebSocket connections
func WebSocketHandler(hub *Hub, w http.ResponseWriter, r *http.Request) {
	// Authenticate the WebSocket connection
	username, userID, email, err := AuthenticateWebSocket(r)
	if err != nil {
		log.Error().Err(err).Msg("WebSocket authentication failed")
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade connection")
		return
	}

	// Record new connection
	RecordConnection(Conf.Realm)

	// Create new client
	client := &Client{
		Username: username,
		UserID:   userID,
		Email:    email,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		Hub:      hub,
	}

	// Start goroutines for reading and writing
	go client.writePump()
	go client.readPump()
}

// HealthHandler provides a health check endpoint
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := map[string]string{
		"status":  "healthy",
		"service": "turnip",
		"version": Conf.Version,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode health check JSON response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
