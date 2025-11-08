package gdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// MaxPageSize is the maximum number of files to retrieve per request.
const MaxPageSize = 100

// DriveClient wraps the Google Drive API client.
// Safe for concurrent use by multiple goroutines.
type DriveClient struct {
	service *drive.Service
}

// FileInfo represents metadata about a Google Drive file.
type FileInfo struct {
	ID          string // Unique file identifier
	Name        string // File name
	MimeType    string // MIME type of the file
	Size        int64  // Size in bytes
	WebViewLink string // URL to view file in browser
}

// newDriveClient is the internal helper to initialize the Google Drive service.
func newDriveClient(ctx context.Context, client *http.Client) (*DriveClient, error) {
	// Only use DriveReadonlyScope for API initialization.
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("unable to create Drive service: %w", err)
	}
	return &DriveClient{service: srv}, nil
}

// NewDriveClientForServiceAccount creates a DriveClient using the content of a
// Service Account JSON credentials file. This is ideal for server-to-server interaction.
func NewDriveClientForServiceAccount(ctx context.Context, jsonCredentials []byte) (*DriveClient, error) {
	// The scope is restricted to read-only access.
	config, err := google.JWTConfigFromJSON(jsonCredentials, drive.DriveReadonlyScope)
	if err != nil {
		return nil, fmt.Errorf("unable to parse service account credentials: %w", err)
	}
	client := config.Client(ctx)
	return newDriveClient(ctx, client)
}

// NewDriveClientWithToken creates a DriveClient using an existing, valid OAuth2 token.
// This is the typical way a web application's backend initializes the client
// after successfully completing the OAuth2 handshake.
func NewDriveClientWithToken(ctx context.Context, config *oauth2.Config, tok *oauth2.Token) (*DriveClient, error) {
	client := config.Client(ctx, tok)
	return newDriveClient(ctx, client)
}

// GetConfigFromJSON parses OAuth2 user credentials JSON into an oauth2.Config.
// This config is used by the web app to generate the AuthCodeURL and exchange the code.
func GetConfigFromJSON(jsonCredentials []byte) (*oauth2.Config, error) {
	return google.ConfigFromJSON(jsonCredentials, drive.DriveReadonlyScope)
}

// ListFiles retrieves all non-folder files from Google Drive.
func (dc *DriveClient) ListFiles(ctx context.Context) ([]FileInfo, error) {
	files := make([]FileInfo, 0, MaxPageSize)
	pageToken := ""

	for {
		call := dc.service.Files.List().
			Context(ctx).
			PageSize(MaxPageSize).
			Fields("nextPageToken, files(id, name, mimeType, size, webViewLink)")

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		r, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve files: %w", err)
		}

		for _, item := range r.Files {
			if item.Size == 0 || item.MimeType == "application/vnd.google-apps.folder" {
				continue
			}

			files = append(files, FileInfo{
				ID:          item.Id,
				Name:        item.Name,
				MimeType:    item.MimeType,
				Size:        item.Size,
				WebViewLink: item.WebViewLink,
			})
		}

		pageToken = r.NextPageToken
		if pageToken == "" {
			break
		}
	}

	return files, nil
}

// StreamFile downloads a file from Google Drive and streams its content to the provided io.Writer.
// This is highly efficient for web responses (e.g., writing to an http.ResponseWriter).
// Returns the number of bytes written and an error if the operation fails.
func (dc *DriveClient) StreamFile(ctx context.Context, fileID string, w io.Writer) (int64, error) {
	if fileID == "" {
		return 0, errors.New("file ID cannot be empty")
	}

	resp, err := dc.service.Files.Get(fileID).Context(ctx).Download()
	if err != nil {
		return 0, fmt.Errorf("unable to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Log or read the response body for more detailed error info if needed.
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		return written, fmt.Errorf("unable to stream file content: %w", err)
	}

	return written, nil
}

// DownloadFile is a convenience function that streams a file to a local path.
// Returns the number of bytes written and an error if the operation fails.
func (dc *DriveClient) DownloadFile(ctx context.Context, fileID, outputPath string) (int64, error) {
	if outputPath == "" {
		return 0, errors.New("output path cannot be empty")
	}

	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("unable to create output directory: %w", err)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return 0, fmt.Errorf("unable to create output file: %w", err)
	}
	defer out.Close()

	// Use StreamFile to perform the actual download.
	written, err := dc.StreamFile(ctx, fileID, out)
	if err != nil {
		return written, fmt.Errorf("unable to download file: %w", err)
	}

	return written, nil
}
