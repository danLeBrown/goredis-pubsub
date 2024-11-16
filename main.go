package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

type Message struct {
	ID             int    `json:"id"`
	SenderID       int    `json:"sender_id"`
	Body           string `json:"body"`
	ConversationID int    `json:"conversation_id"`
}

type InAppNotification struct {
	ID         int    `json:"id"`
	PlayerID   int    `json:"player_id"`
	ResourceID int    `json:"resource_id"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Meta       string `json:"meta"`
	ReadAt     string `json:"read_at"`
}

type FriendRequest struct {
	ID         int    `json:"id"`
	SenderID   int    `json:"sender_id"`
	ReceiverID int    `json:"receiver_id"`
	Status     string `json:"status"`
	AcceptedAt string `json:"accepted_at"`
	RejectedAt string `json:"rejected_at"`
}

// SSEConfig holds configuration for SSE handlers
type SSEConfig struct {
	redisClient  *redis.Client
	channelName  string
	eventType    string
	unmarshaller func([]byte) (interface{}, error)
}

// RedisConfig holds Redis connection configuration
type RedisConfig struct {
	addr     string
	password string
}

func main() {
	redisConfig := loadConfig()
	redisClient := newRedisClient(redisConfig)
	defer redisClient.Close()

	// Configure message stream
	messageConfig := &SSEConfig{
		redisClient: redisClient,
		channelName: "game_evo_dev.messages",
		eventType:   "message",
		unmarshaller: func(data []byte) (interface{}, error) {
			var msg Message
			err := json.Unmarshal(data, &msg)
			return msg, err
		},
	}

	// Configure notification stream
	notificationConfig := &SSEConfig{
		redisClient: redisClient,
		channelName: "game_evo_dev.notifications",
		eventType:   "notification",
		unmarshaller: func(data []byte) (interface{}, error) {
			var notification InAppNotification
			err := json.Unmarshal(data, &notification)
			return notification, err
		},
	}

	// Configure friend request stream
	friendRequestConfig := &SSEConfig{
		redisClient: redisClient,
		channelName: "game_evo_dev.friend_request",
		eventType:   "friend_request",
		unmarshaller: func(data []byte) (interface{}, error) {
			var friendRequest FriendRequest
			err := json.Unmarshal(data, &friendRequest)
			return friendRequest, err
		},
	}

	// Set up routes
	http.HandleFunc("/stream/messages", createSSEHandler(messageConfig))
	http.HandleFunc("/stream/notifications", createSSEHandler(notificationConfig))
	http.HandleFunc("/stream/friend-requests", createSSEHandler(friendRequestConfig))

	port := getEnvOrDefault("PORT", "8080")
	log.Printf("Server starting on port %s", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
}

func loadConfig() RedisConfig {
	envFile, _ := godotenv.Read(".env")

	return RedisConfig{
		addr:     getEnvOrFileValue(envFile, "REDIS_ADDR"),
		password: getEnvOrFileValue(envFile, "REDIS_PASSWORD"),
	}
}

func getEnvOrFileValue(envFile map[string]string, key string) string {
	if value := envFile[key]; value != "" {
		return value
	}
	return os.Getenv(key)
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func newRedisClient(config RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:        config.addr,
		Password:    config.password,
		MaxRetries:  3,
		PoolTimeout: 30 * time.Second,
	})
}

func createSSEHandler(config *SSEConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isSSESupported(r) {
			http.Error(w, "SSE not supported", http.StatusNotAcceptable)
			return
		}

		setupSSEHeaders(w)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Monitor client connection
		go func() {
			<-ctx.Done()
			log.Printf("Client disconnected from %s stream", config.eventType)
		}()

		if err := config.redisClient.Ping(ctx).Err(); err != nil {
			log.Printf("Redis connection error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		pubsub := config.redisClient.Subscribe(ctx, config.channelName)
		defer pubsub.Close()

		// Send initial connection message
		if err := sendSSEMessage(w, "connected", fmt.Sprintf("Connected to %s stream", config.eventType)); err != nil {
			log.Printf("Failed to send initial message: %v", err)
			return
		}

		handleSSEStream(ctx, w, pubsub, config)
	}
}

func handleSSEStream(ctx context.Context, w http.ResponseWriter, pubsub *redis.PubSub, config *SSEConfig) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sendSSEComment(w, "keep-alive"); err != nil {
				log.Printf("Failed to send keep-alive: %v", err)
				return
			}
		case msg := <-pubsub.Channel():
			if _, err := config.unmarshaller([]byte(msg.Payload)); err != nil {
				log.Printf("Failed to unmarshal %s: %v", config.eventType, err)
				continue
			}

			if err := sendSSEMessage(w, "message", msg.Payload); err != nil {
				log.Printf("Failed to send %s: %v", config.eventType, err)
				return
			}
		}
	}
}

func setupSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func isSSESupported(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept == "" || accept == "text/event-stream"
}

func sendSSEMessage(w http.ResponseWriter, event, data string) error {
	_, err := w.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)))
	if err != nil {
		return err
	}

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func sendSSEComment(w http.ResponseWriter, comment string) error {
	_, err := w.Write([]byte(fmt.Sprintf(": %s\n\n", comment)))
	if err != nil {
		return err
	}

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
