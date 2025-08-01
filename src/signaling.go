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
		if err := c.Conn.Close(); err != nil {
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
				if username, ok := message["username"].(string); ok {
					if room, ok := message["room"].(string); ok {
						c.Username = username
						c.Room = room
						c.Hub.Register <- c
						RecordRoomJoin(Conf.Realm)
						log.Info().
							Str("username", username).
							Str("room", room).
							Msg("Client requested to join room")
					}
				}

			case "data":
				// Handle data message
				if data, ok := message["data"]; ok {
					log.Info().
						Str("username", c.Username).
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
			}
		}
	}
}

// writePump pumps messages from the hub to the websocket connection
func (c *Client) writePump() {
	defer func() {
		if err := c.Conn.Close(); err != nil {
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
		"service": "turnip-signaling",
		"version": Conf.Version,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode health check JSON response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
