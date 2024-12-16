package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

type Claims struct {
	jwt.RegisteredClaims
	Role     string `json:"role"`
	UserID   string `json:"uid"`
	PlayerID string `json:"pid"`
}

type AuthConfig struct {
	secretKey []byte
}

type PlayerMessage struct {
	Participants []int   `json:"participants"`
	Data         Message `json:"data"`
}

type Message struct {
	ID             int    `json:"id"`
	SenderID       int    `json:"sender_id"`
	Body           string `json:"body"`
	ConversationID int    `json:"conversation_id"`
}

// the notification payload could be different depending on the key
type Notification[T any] struct {
	Type     string `json:"type"`
	Data     T      `json:"data"`
	PlayerID string `json:"player_id"`
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
	prefix   string
}

var logger = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)

// func
func main() {
	// Create a new log file
	file, err := os.OpenFile("game-evo.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// Set the log output to the log file
	logger.SetOutput(file)

	// Set log flags to include date and time
	// log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Log a sample message
	logger.Println("Logging started")

	redisConfig := &RedisConfig{}
	authConfig := &AuthConfig{}
	loadConfig(redisConfig, authConfig)

	redisClient := newRedisClient(*redisConfig)
	defer redisClient.Close()

	// Configure message stream
	// messageConfig := &SSEConfig{
	// 	redisClient: redisClient,
	// 	channelName: fmt.Sprintf("%v.messages", redisConfig.prefix),
	// 	eventType:   "message",
	// 	unmarshaller: func(data []byte) (interface{}, error) {
	// 		var msg Message
	// 		err := json.Unmarshal(data, &msg)
	// 		return msg, err
	// 	},
	// }

	// Configure notification stream
	// notificationConfig := &SSEConfig{
	// 	redisClient: redisClient,
	// 	channelName: fmt.Sprintf("%v.notifications", redisConfig.prefix),
	// 	eventType:   "notification",
	// 	unmarshaller: func(data []byte) (interface{}, error) {
	// 		var notification InAppNotification
	// 		err := json.Unmarshal(data, &notification)
	// 		return notification, err
	// 	},
	// }

	// Configure friend request stream
	// friendRequestConfig := &SSEConfig{
	// 	redisClient: redisClient,
	// 	channelName: fmt.Sprintf("%v.friend_request", redisConfig.prefix),
	// 	eventType:   "friend_request",
	// 	unmarshaller: func(data []byte) (interface{}, error) {
	// 		var friendRequest FriendRequest
	// 		err := json.Unmarshal(data, &friendRequest)
	// 		return friendRequest, err
	// 	},
	// }

	playerNotificationConfig := &SSEConfig{
		redisClient: redisClient,
		channelName: fmt.Sprintf("%v.player.notifications", redisConfig.prefix),
		eventType:   "notification",
		unmarshaller: func(data []byte) (interface{}, error) {
			var notification Notification[any]
			err := json.Unmarshal(data, &notification)
			return notification, err
		},
	}

	playerMessageConfig := &SSEConfig{
		redisClient: redisClient,
		channelName: fmt.Sprintf("%v.player.messages", redisConfig.prefix),
		eventType:   "message",
		unmarshaller: func(data []byte) (interface{}, error) {
			var notification PlayerMessage
			err := json.Unmarshal(data, &notification)
			return notification, err
		},
	}

	// conversationConfig := &SSEConfig{
	// 	redisClient: redisClient,
	// 	channelName: fmt.Sprintf("%v.conversation", redisConfig.prefix),
	// 	eventType:   "conversation",
	// 	unmarshaller: func(data []byte) (interface{}, error) {
	// 		var friendRequest FriendRequest
	// 		err := json.Unmarshal(data, &friendRequest)
	// 		return friendRequest, err
	// 	},
	// }

	// Set up routes
	// http.HandleFunc("/stream/messages", createSSEHandler(messageConfig, authConfig))
	// http.HandleFunc("/stream/messages/conversations", createSSEHandler(messageConfig, authConfig))
	// http.HandleFunc("/stream/notifications", createSSEHandler(notificationConfig, authConfig))
	// http.HandleFunc("/stream/notifications/friend-requests", createSSEHandler(notificationConfig, authConfig))
	// http.HandleFunc("/stream/friend-requests", createSSEHandler(friendRequestConfig, authConfig))
	http.HandleFunc("/stream/player/notifications", createSSEHandler(playerNotificationConfig, authConfig))
	http.HandleFunc("/stream/player/messages", createSSEHandler(playerMessageConfig, authConfig))

	port := getEnvOrDefault("PORT", "8080")
	logger.Printf("Server starting on port %s", port)
	logger.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
}

func validateToken(tokenString string, config *AuthConfig) (*Claims, error) {
	// For debugging
	logger.Printf("Validating token: %s", tokenString)
	logger.Printf("Using secret: %s", string(config.secretKey))

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return config.secretKey, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

func loadConfig(redisConfig *RedisConfig, authConfig *AuthConfig) {
	envFile, _ := godotenv.Read(".env")

	redisConfig.addr = getEnvOrFileValue(envFile, "REDIS_ADDR")
	redisConfig.password = getEnvOrFileValue(envFile, "REDIS_PASSWORD")
	redisConfig.prefix = getEnvOrFileValue(envFile, "REDIS_PREFIX")

	authConfig.secretKey = []byte(getEnvOrFileValue(envFile, "STREAM_SECRET"))
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

func createSSEHandler(config *SSEConfig, authConfig *AuthConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Printf("Creating SSE handler for %s", config.channelName)
		// Get token from query param

		tokenString := r.URL.Query().Get("token")
		if tokenString == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		claims, err := validateToken(tokenString, authConfig)
		logger.Printf("Error: %v", err)
		if err != nil {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		userID := r.URL.Query().Get("uid")
		logger.Printf("UserID: %s", userID)

		if userID == "" {
			http.Error(w, "Unprocessable entity", http.StatusUnprocessableEntity)
			return
		}

		if claims.UserID != userID {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		playerID := r.URL.Query().Get("pid")
		if playerID == "" && claims.Role == "u" {
			http.Error(w, "Unprocessable entity", http.StatusUnprocessableEntity)
			return
		}

		if claims.PlayerID != playerID && claims.Role == "u" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// log the claims
		logger.Printf("Claims: %v", claims)

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
			logger.Printf("Client disconnected from %s stream", config.eventType)
		}()

		if err := config.redisClient.Ping(ctx).Err(); err != nil {
			logger.Printf("Redis connection error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		pubsub := config.redisClient.Subscribe(ctx, config.channelName)
		defer pubsub.Close()

		// Send initial connection message
		if err := sendSSEMessage(w, "connected", fmt.Sprintf("Connected to %s stream", config.eventType)); err != nil {
			logger.Printf("Failed to send initial message: %v", err)
			return
		}

		handleSSEStream(ctx, w, pubsub, config, playerID)
	}
}

func handleSSEStream(ctx context.Context, w http.ResponseWriter, pubsub *redis.PubSub, config *SSEConfig, playerID string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sendSSEComment(w, "keep-alive"); err != nil {
				logger.Printf("Failed to send keep-alive: %v", err)
				return
			}
		case msg := <-pubsub.Channel():
			switch config.eventType {
			case "notification":
				handlePlayerNotificationStream(w, config, msg, playerID)
			case "message":
				handlePlayerMessageStream(w, config, msg, playerID)
			default:
				_, err := config.unmarshaller([]byte(msg.Payload))
				if err != nil {
					logger.Printf("Failed to unmarshal %s: %v", config.eventType, err)
					continue
				}

				if err := sendSSEMessage(w, config.eventType, msg.Payload); err != nil {
					logger.Printf("Failed to send %s: %v", config.eventType, err)
					return
				}
			}
		}
	}
}

func handlePlayerNotificationStream(w http.ResponseWriter, config *SSEConfig, msg *redis.Message, playerID string) {
	var notification struct {
		PlayerID string `json:"player_id"`
		Type     string `json:"type"`
	}

	err := json.Unmarshal([]byte(msg.Payload), &notification)
	if err != nil {
		logger.Printf("Failed to unmarshal %s: %v", config.eventType, err)
		return
	}

	if notification.PlayerID != playerID {
		return
	}

	if err := sendSSEMessage(w, notification.Type, msg.Payload); err != nil {

		logger.Printf("Failed to send %s: %v", config.eventType, err)
		return
	}
}

func handlePlayerMessageStream(w http.ResponseWriter, config *SSEConfig, msg *redis.Message, playerID string) {
	var message PlayerMessage
	err := json.Unmarshal([]byte(msg.Payload), &message)
	if err != nil {
		logger.Printf("Failed to unmarshal %s: %v", config.eventType, err)
		return
	}

	participantID, err := strconv.Atoi(playerID)
	if err != nil {
		logger.Printf("Failed to convert playerID to int: %v", err)
		return
	}

	if !slices.Contains(message.Participants, participantID) {
		return
	}

	if err := sendSSEMessage(w, config.eventType, msg.Payload); err != nil {
		logger.Printf("Failed to send %s: %v", config.eventType, err)
		return
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
