package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"os"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

type Message struct {
	ID             int    `json:"id"`
	SenderID       int    `json:"sender_id"`
	Body           string `json:"body"`
	ConversationID int    `json:"conversation_id"`
}

var redisAddr string
var redisPassword string

func main() {
	envFile, _ := godotenv.Read(".env")
	redisAddr = envFile["REDIS_ADDR"]
	redisPassword = envFile["REDIS_PASSWORD"]

	if redisAddr == "" {
		redisAddr = os.Getenv("REDIS_URL")
	}

	if redisPassword == "" {
		redisPassword = os.Getenv("REDIS_PASSWORD")
	}

	port := os.Getenv("PORT")

	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/stream", handleStream)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
}

func handleStream(w http.ResponseWriter, r *http.Request) {
	// Check if client supports SSE
	if !isSSESupported(r) {
		http.Error(w, "SSE not supported", http.StatusNotAcceptable)
		return
	}

	// Headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*") // Add CORS if needed

	// Create a context that's canceled when the client disconnects
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Monitor client connection
	go func() {
		<-ctx.Done()
		log.Println("Client disconnected")
	}()

	// "redis-12567.c15.us-east-1-4.ec2.redns.redis-cloud.com:12567"
	// Connect to Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:        redisAddr,
		Password:    redisPassword,
		MaxRetries:  3,
		PoolTimeout: 30 * time.Second,
	})
	defer rdb.Close()

	// Test Redis connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("Redis connection error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Subscribe to Redis channel
	pubsub := rdb.Subscribe(ctx, "game_evo_dev.chat")
	defer pubsub.Close()

	// Send initial connection established message
	sendSSEMessage(w, "connected", "Connection established")

	// Keep-alive ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Send keep-alive comment
			if err := sendSSEComment(w, "keep-alive"); err != nil {
				log.Printf("Failed to send keep-alive: %v", err)
				return
			}
		case msg := <-pubsub.Channel():
			var message Message
			if err := json.Unmarshal([]byte(msg.Payload), &message); err != nil {
				log.Printf("Failed to unmarshal message: %v", err)
				continue
			}

			// Send the message with an event type
			if err := sendSSEMessage(w, "message", msg.Payload); err != nil {
				log.Printf("Failed to send message: %v", err)
				return
			}
		}
	}
}

func isSSESupported(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept == "" || accept == "text/event-stream"
}

func sendSSEMessage(w http.ResponseWriter, event, data string) error {
	_, err := w.Write([]byte("event: " + event + "\ndata: " + data + "\n\n"))
	if err != nil {
		return err
	}

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func sendSSEComment(w http.ResponseWriter, comment string) error {
	_, err := w.Write([]byte(": " + comment + "\n\n"))
	if err != nil {
		return err
	}

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
