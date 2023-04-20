package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
)

// Server to intercept access_token from redirect url.
type AccessTokenServer struct {
	port string // Port to listen on
}

func NewTokenServer(port string) *AccessTokenServer {
	return &AccessTokenServer{port: port}
}

// Runs an http server to intercept the client token sent from the browser.
// When token arrives, it's written onto the channel and server shutdown.
// If context expires, the server should also exit with an error.
func (s *AccessTokenServer) Run(ctx context.Context, tokenChan chan string) {
	// Create a new HTTP server
	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:    fmt.Sprintf("localhost:%s", s.port),
		Handler: mux,
	}

	// Create a new handler function to handle the incoming HTTP requests
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Get the access token from the query parameters
		accessToken := r.URL.Query().Get("code")

		// Write the access token onto the channel
		tokenChan <- accessToken

		// Send a response back to the client
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<h1>Access token received</h1>"))

		// Shut down the server
		srv.Shutdown(ctx)
	})

	// Start the HTTP server in a goroutine
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server exited with error: %v\n", err)
	}

}
