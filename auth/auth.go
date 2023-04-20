package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/abiiranathan/gdrive/server"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v2"
)

// Configures authentication to google cloud.
type GoogleAuth struct {
	// Path to credentials.json for your google cloud API.
	CredentialFile string
	// Where to PoST the token following authentication.
	RedirectURL string
	// filename where to save the token(or where token.json is located)
	TokenFile string
	// The TokenServer to handle access token requests.
	TokenServer *server.AccessTokenServer
}

// Reads credentials.json and configures an *http.Client with
// the access_token. If the access token does not exist, its created(required the signin with google prompt)
func (auth *GoogleAuth) GetClient() (*http.Client, error) {
	// Read the Google API credentials from a file
	credentials, err := os.ReadFile(auth.CredentialFile)

	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file: %v", err)
	}

	// Set up the Google OAuth2 config
	config, err := google.ConfigFromJSON(credentials, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials file: %v", err)
	}

	// Set the redirect url
	config.RedirectURL = auth.RedirectURL

	// Get a new token
	token, err := auth.getToken(config, auth.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %v", err)
	}

	// Create a new HTTP client with the token
	return config.Client(context.Background(), token), nil
}

func (auth *GoogleAuth) getToken(config *oauth2.Config, tokenFile string) (*oauth2.Token, error) {
	token, err := tokenFromFile(tokenFile)
	if err == nil {
		return token, nil
	}

	// Get a new token from the user
	token = auth.getTokenFromWeb(config)
	if token == nil {
		return nil, fmt.Errorf("failed to get token from web")
	}

	// Save the token to a file for later use
	err = saveToken(tokenFile, token)
	if err != nil {
		return nil, fmt.Errorf("failed to save token: %v", err)
	}
	return token, nil
}

func tokenFromFile(file string) (*oauth2.Token, error) {
	// Try to read the token from a file
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	token := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(token)
	return token, err
}

func saveToken(file string, token *oauth2.Token) error {
	// Save the token to a file
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()
	err = json.NewEncoder(f).Encode(token)
	return err
}

func (auth *GoogleAuth) getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	// Get a new authorization code from the user
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser: \n%v\n\n", authURL)

	tokenChan := make(chan string)
	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Minute*2)
	defer cancelFunc()

	// Start an http server in a go routine.
	// Wait on the access token channel for 2 minutes.
	go auth.TokenServer.Run(ctx, tokenChan)

	fmt.Println("Waiting for access token...")
	var access_token string
	select {
	case access_token = <-tokenChan:
	case <-ctx.Done():
		close(tokenChan)
		log.Fatalf("timeout: %v", ctx.Err())
	}

	// Exchange the authorization code for a token
	token, err := config.Exchange(context.Background(), access_token)
	if err != nil {
		log.Fatalf("Failed to exchange authorization code for token: %v\n", err)
	}
	return token
}
