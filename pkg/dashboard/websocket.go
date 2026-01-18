package dashboard

import (
	"sync"
)

// Client represents a WebSocket client connection.
type Client struct {
	hub    *Hub
	taskID string // Which task this client is subscribed to (empty = all)
	send   chan []byte
}

// Hub manages WebSocket client connections and message broadcasting.
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan broadcastMsg
	register   chan *Client
	unregister chan *Client
	stop       chan struct{}
	mu         sync.RWMutex
}

type broadcastMsg struct {
	taskID string
	data   []byte
}

// NewHub creates a new WebSocket hub.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan broadcastMsg, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		stop:       make(chan struct{}),
	}
}

// Run starts the hub's main loop.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				// Send to clients subscribed to this task or to all tasks
				if client.taskID == "" || client.taskID == msg.taskID {
					select {
					case client.send <- msg.data:
					default:
						// Client buffer full, skip
					}
				}
			}
			h.mu.RUnlock()

		case <-h.stop:
			h.mu.Lock()
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			h.mu.Unlock()
			return
		}
	}
}

// Stop shuts down the hub.
func (h *Hub) Stop() {
	close(h.stop)
}

// Register adds a client to the hub.
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// Broadcast sends a message to all clients subscribed to a task.
func (h *Hub) Broadcast(taskID string, data []byte) {
	select {
	case h.broadcast <- broadcastMsg{taskID: taskID, data: data}:
	default:
		// Broadcast buffer full, drop message
	}
}

// BroadcastAll sends a message to all connected clients.
func (h *Hub) BroadcastAll(data []byte) {
	h.Broadcast("", data)
}
