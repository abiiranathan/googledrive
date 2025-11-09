package gdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
// FileInfo represents metadata about a Google Drive file.
type FileInfo struct {
	ID          string   // Unique file identifier
	Name        string   // File name
	MimeType    string   // MIME type of the file
	Size        int64    // Size in bytes
	WebViewLink string   // URL to view file in browser
	Parents     []string // Parent folder IDs
	FolderPath  string   // Full folder path (e.g., "My Drive/Projects/2024")
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

// ListFiles retrieves all non-folder files from Google Drive with folder information.
func (dc *DriveClient) ListFiles(ctx context.Context) ([]FileInfo, error) {
	files := make([]FileInfo, 0, MaxPageSize)
	pageToken := ""

	// First, build a map of folder IDs to folder names
	folderMap := make(map[string]string)

	// Fetch all folders
	foldersCall := dc.service.Files.List().
		Context(ctx).
		Q("mimeType='application/vnd.google-apps.folder'").
		Fields("files(id, name, parents)").
		PageSize(1000)

	foldersResp, err := foldersCall.Do()
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve folders: %w", err)
	}

	for _, folder := range foldersResp.Files {
		folderMap[folder.Id] = folder.Name
	}

	// Helper function to build folder path
	buildPath := func(parentIDs []string) string {
		if len(parentIDs) == 0 {
			return "My Drive"
		}

		var pathParts []string
		currentID := parentIDs[0]
		visited := make(map[string]bool)

		// Traverse up the folder hierarchy (max 10 levels to prevent infinite loops)
		for i := 0; i < 10 && currentID != "" && !visited[currentID]; i++ {
			visited[currentID] = true
			if folderName, exists := folderMap[currentID]; exists {
				pathParts = append([]string{folderName}, pathParts...)
				// Find parent of current folder
				for _, folder := range foldersResp.Files {
					if folder.Id == currentID && len(folder.Parents) > 0 {
						currentID = folder.Parents[0]
						break
					}
				}
			} else {
				break
			}
		}

		if len(pathParts) == 0 {
			return "My Drive"
		}
		return "My Drive/" + strings.Join(pathParts, "/")
	}

	// Now fetch all files
	for {
		call := dc.service.Files.List().
			Context(ctx).
			PageSize(MaxPageSize).
			Fields("nextPageToken, files(id, name, mimeType, size, webViewLink, parents)")

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
				Parents:     item.Parents,
				FolderPath:  buildPath(item.Parents),
			})
		}

		pageToken = r.NextPageToken
		if pageToken == "" {
			break
		}
	}

	return files, nil
}

// ListFilesInFolder retrieves all non-folder files from a specific Google Drive folder.
// If parentFolderID is empty, lists files in the root of My Drive.
// Returns files with their folder path information.
func (dc *DriveClient) ListFilesInFolder(ctx context.Context, parentFolderID string) ([]FileInfo, error) {
	files := make([]FileInfo, 0, MaxPageSize)
	pageToken := ""

	// Build query to filter by parent folder
	query := "trashed=false"
	if parentFolderID != "" {
		query = fmt.Sprintf("'%s' in parents and trashed=false", parentFolderID)
	}

	// First, build a map of folder IDs to folder names for path resolution
	folderMap := make(map[string]string)
	folderParentMap := make(map[string][]string)

	// Fetch all folders for path building
	foldersCall := dc.service.Files.List().
		Context(ctx).
		Q("mimeType='application/vnd.google-apps.folder'").
		Fields("files(id, name, parents)").
		PageSize(1000)

	foldersResp, err := foldersCall.Do()
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve folders: %w", err)
	}

	for _, folder := range foldersResp.Files {
		folderMap[folder.Id] = folder.Name
		folderParentMap[folder.Id] = folder.Parents
	}

	// Helper function to build folder path
	buildPath := func(parentIDs []string) string {
		if len(parentIDs) == 0 {
			return "My Drive"
		}

		var pathParts []string
		currentID := parentIDs[0]
		visited := make(map[string]bool)

		// Traverse up the folder hierarchy (max 10 levels to prevent infinite loops)
		for i := 0; i < 10 && currentID != "" && !visited[currentID]; i++ {
			visited[currentID] = true
			if folderName, exists := folderMap[currentID]; exists {
				pathParts = append([]string{folderName}, pathParts...)
				// Move to parent folder
				if parents, hasParent := folderParentMap[currentID]; hasParent && len(parents) > 0 {
					currentID = parents[0]
				} else {
					break
				}
			} else {
				break
			}
		}

		if len(pathParts) == 0 {
			return "My Drive"
		}
		return "My Drive/" + strings.Join(pathParts, "/")
	}

	// Fetch files in the specified folder
	for {
		call := dc.service.Files.List().
			Context(ctx).
			Q(query).
			PageSize(MaxPageSize).
			Fields("nextPageToken, files(id, name, mimeType, size, webViewLink, parents)")

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		r, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve files: %w", err)
		}

		for _, item := range r.Files {
			// Skip folders and zero-size files
			if item.MimeType == "application/vnd.google-apps.folder" || item.Size == 0 {
				continue
			}

			files = append(files, FileInfo{
				ID:          item.Id,
				Name:        item.Name,
				MimeType:    item.MimeType,
				Size:        item.Size,
				WebViewLink: item.WebViewLink,
				Parents:     item.Parents,
				FolderPath:  buildPath(item.Parents),
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

// UploadFile uploads a file to Google Drive.
// If parentFolderID is empty, the file is uploaded to the root of My Drive.
// Returns the file ID of the uploaded file and an error if the operation fails.
func (dc *DriveClient) UploadFile(ctx context.Context, filePath, fileName, parentFolderID string) (string, error) {
	if filePath == "" {
		return "", errors.New("file path cannot be empty")
	}
	if fileName == "" {
		fileName = filepath.Base(filePath)
	}

	// Open the file to upload
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("unable to open file: %w", err)
	}
	defer file.Close()

	// Get file info for size
	fileInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("unable to stat file: %w", err)
	}

	// Detect MIME type
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("unable to read file for MIME detection: %w", err)
	}
	mimeType := http.DetectContentType(buffer[:n])

	// Reset file pointer to beginning
	if _, err := file.Seek(0, 0); err != nil {
		return "", fmt.Errorf("unable to reset file pointer: %w", err)
	}

	// Create file metadata
	fileMeta := &drive.File{
		Name:     fileName,
		MimeType: mimeType,
	}

	// Set parent folder if specified
	if parentFolderID != "" {
		fileMeta.Parents = []string{parentFolderID}
	}

	// Upload the file
	uploadedFile, err := dc.service.Files.Create(fileMeta).
		Context(ctx).
		Media(file).
		Fields("id, name, mimeType, size, parents, webViewLink").
		Do()
	if err != nil {
		return "", fmt.Errorf("unable to upload file: %w", err)
	}

	fmt.Printf("File uploaded successfully: %s (ID: %s, Size: %d bytes)\n",
		uploadedFile.Name, uploadedFile.Id, fileInfo.Size())

	return uploadedFile.Id, nil
}

// UploadFileFromReader uploads a file to Google Drive from an io.Reader.
// This is useful for web applications to upload files from HTTP requests.
// Returns the file ID of the uploaded file and an error if the operation fails.
func (dc *DriveClient) UploadFileFromReader(ctx context.Context, reader io.Reader, fileName, mimeType, parentFolderID string) (string, error) {
	if reader == nil {
		return "", errors.New("reader cannot be nil")
	}
	if fileName == "" {
		return "", errors.New("file name cannot be empty")
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Create file metadata
	fileMeta := &drive.File{
		Name:     fileName,
		MimeType: mimeType,
	}

	// Set parent folder if specified
	if parentFolderID != "" {
		fileMeta.Parents = []string{parentFolderID}
	}

	// Upload the file
	uploadedFile, err := dc.service.Files.Create(fileMeta).
		Context(ctx).
		Media(reader).
		Fields("id, name, mimeType, size, parents, webViewLink").
		Do()
	if err != nil {
		return "", fmt.Errorf("unable to upload file: %w", err)
	}

	fmt.Printf("File uploaded successfully: %s (ID: %s)\n", uploadedFile.Name, uploadedFile.Id)
	return uploadedFile.Id, nil
}

// CreateFolder creates a new folder in Google Drive.
// If parentFolderID is empty, the folder is created in the root of My Drive.
// Returns the folder ID and an error if the operation fails.
func (dc *DriveClient) CreateFolder(ctx context.Context, folderName, parentFolderID string) (string, error) {
	if folderName == "" {
		return "", errors.New("folder name cannot be empty")
	}

	// Create folder metadata
	folderMeta := &drive.File{
		Name:     folderName,
		MimeType: "application/vnd.google-apps.folder",
	}

	// Set parent folder if specified
	if parentFolderID != "" {
		folderMeta.Parents = []string{parentFolderID}
	}

	// Create the folder
	folder, err := dc.service.Files.Create(folderMeta).
		Context(ctx).
		Fields("id, name").
		Do()
	if err != nil {
		return "", fmt.Errorf("unable to create folder: %w", err)
	}

	fmt.Printf("Folder created successfully: %s (ID: %s)\n", folder.Name, folder.Id)
	return folder.Id, nil
}

// TrashFile moves a file to the trash in Google Drive.
// The file can be restored from trash later.
// Returns an error if the operation fails.
func (dc *DriveClient) TrashFile(ctx context.Context, fileID string) error {
	if fileID == "" {
		return errors.New("file ID cannot be empty")
	}

	// Update the file to set trashed=true
	_, err := dc.service.Files.Update(fileID, &drive.File{
		Trashed: true,
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("unable to trash file: %w", err)
	}

	fmt.Printf("File moved to trash: %s\n", fileID)
	return nil
}

// RestoreFile restores a file from the trash in Google Drive.
// Returns an error if the operation fails.
func (dc *DriveClient) RestoreFile(ctx context.Context, fileID string) error {
	if fileID == "" {
		return errors.New("file ID cannot be empty")
	}

	// Update the file to set trashed=false
	_, err := dc.service.Files.Update(fileID, &drive.File{
		Trashed: false,
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("unable to restore file: %w", err)
	}

	fmt.Printf("File restored from trash: %s\n", fileID)
	return nil
}

// DeleteFile permanently deletes a file from Google Drive.
// WARNING: This action is irreversible. The file cannot be recovered.
// Returns an error if the operation fails.
func (dc *DriveClient) DeleteFile(ctx context.Context, fileID string) error {
	if fileID == "" {
		return errors.New("file ID cannot be empty")
	}

	err := dc.service.Files.Delete(fileID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("unable to delete file permanently: %w", err)
	}

	fmt.Printf("File permanently deleted: %s\n", fileID)
	return nil
}

// PartialDownloadOptions specifies options for partial file downloads.
type PartialDownloadOptions struct {
	StartByte int64 // Starting byte position (inclusive)
	EndByte   int64 // Ending byte position (inclusive)
}

// PartialDownloadFile downloads a specific byte range of a file from Google Drive.
// This is useful for resumable downloads or streaming large files in chunks.
// Note: Partial downloads are not supported for Google Workspace documents.
// Returns the number of bytes written and an error if the operation fails.
func (dc *DriveClient) PartialDownloadFile(ctx context.Context, fileID string, w io.Writer, opts PartialDownloadOptions) (int64, error) {
	if fileID == "" {
		return 0, errors.New("file ID cannot be empty")
	}
	if opts.StartByte < 0 || opts.EndByte < 0 {
		return 0, errors.New("byte positions cannot be negative")
	}
	if opts.StartByte > opts.EndByte {
		return 0, errors.New("start byte must be less than or equal to end byte")
	}

	call := dc.service.Revisions.Get(fileID, fileID).Context(ctx)

	// Set the Range header for partial download
	rangeHeader := fmt.Sprintf("bytes=%d-%d", opts.StartByte, opts.EndByte)
	call.Header().Set("Range", rangeHeader)
	resp, err := call.Download()
	if err != nil {
		return 0, fmt.Errorf("unable to download revision: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		return written, fmt.Errorf("unable to write revision content: %w", err)
	}

	return written, nil

}

// PartialStreamFile is a convenience wrapper around PartialDownloadFile for streaming.
// Returns the number of bytes written and an error if the operation fails.
func (dc *DriveClient) PartialStreamFile(ctx context.Context, fileID string, w io.Writer, startByte, endByte int64) (int64, error) {
	return dc.PartialDownloadFile(ctx, fileID, w, PartialDownloadOptions{
		StartByte: startByte,
		EndByte:   endByte,
	})
}

// ExportFormat represents supported export formats for Google Workspace documents.
type ExportFormat string

const (
	// PDF exports
	ExportFormatPDF ExportFormat = "application/pdf"

	// Microsoft Office formats
	ExportFormatDOCX ExportFormat = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	ExportFormatXLSX ExportFormat = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	ExportFormatPPTX ExportFormat = "application/vnd.openxmlformats-officedocument.presentationml.presentation"

	// Open Document formats
	ExportFormatODT ExportFormat = "application/vnd.oasis.opendocument.text"
	ExportFormatODS ExportFormat = "application/vnd.oasis.opendocument.spreadsheet"
	ExportFormatODP ExportFormat = "application/vnd.oasis.opendocument.presentation"

	// Rich Text and Plain Text
	ExportFormatRTF  ExportFormat = "application/rtf"
	ExportFormatTXT  ExportFormat = "text/plain"
	ExportFormatHTML ExportFormat = "text/html"
	ExportFormatZIP  ExportFormat = "application/zip"

	// Image formats
	ExportFormatJPEG ExportFormat = "image/jpeg"
	ExportFormatPNG  ExportFormat = "image/png"
	ExportFormatSVG  ExportFormat = "image/svg+xml"

	// CSV for Sheets
	ExportFormatCSV ExportFormat = "text/csv"

	// EPUB for Docs
	ExportFormatEPUB ExportFormat = "application/epub+zip"
)

// ExportWorkspaceDocument exports a Google Workspace document to the specified format.
// Supported formats depend on the document type (Docs, Sheets, Slides, etc.).
// Exported content is limited to 10 MB.
// Returns the number of bytes written and an error if the operation fails.
func (dc *DriveClient) ExportWorkspaceDocument(ctx context.Context, fileID string, w io.Writer, format ExportFormat) (int64, error) {
	if fileID == "" {
		return 0, errors.New("file ID cannot be empty")
	}
	if format == "" {
		return 0, errors.New("export format cannot be empty")
	}

	resp, err := dc.service.Files.Export(fileID, string(format)).Context(ctx).Download()
	if err != nil {
		return 0, fmt.Errorf("unable to export document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		return written, fmt.Errorf("unable to write exported content: %w", err)
	}
	return written, nil
}

// ExportWorkspaceDocumentToFile exports a Google Workspace document to a local file.
// This is a convenience method that wraps ExportWorkspaceDocument.
// Returns the number of bytes written and an error if the operation fails.
func (dc *DriveClient) ExportWorkspaceDocumentToFile(ctx context.Context, fileID, outputPath string, format ExportFormat) (int64, error) {
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

	written, err := dc.ExportWorkspaceDocument(ctx, fileID, out, format)
	if err != nil {
		return written, fmt.Errorf("unable to export document: %w", err)
	}

	return written, nil
}

// GetExportLinks retrieves all available export links for a Google Workspace document.
// Returns a map of MIME type to download URL.
// Returns an error if the file is not a Google Workspace document.
func (dc *DriveClient) GetExportLinks(ctx context.Context, fileID string) (map[string]string, error) {
	if fileID == "" {
		return nil, errors.New("file ID cannot be empty")
	}

	file, err := dc.service.Files.Get(fileID).
		Context(ctx).
		Fields("exportLinks, mimeType").
		Do()
	if err != nil {
		return nil, fmt.Errorf("unable to get file metadata: %w", err)
	}

	if len(file.ExportLinks) == 0 {
		return nil, fmt.Errorf("file is not a Google Workspace document (MIME type: %s)", file.MimeType)
	}
	return file.ExportLinks, nil
}

// DownloadRevision downloads a specific revision of a blob file.
// The revision must be marked as "Keep Forever" to be downloadable.
// Returns the number of bytes written and an error if the operation fails.
func (dc *DriveClient) DownloadRevision(ctx context.Context, fileID, revisionID string, w io.Writer) (int64, error) {
	if fileID == "" {
		return 0, errors.New("file ID cannot be empty")
	}
	if revisionID == "" {
		return 0, errors.New("revision ID cannot be empty")
	}

	resp, err := dc.service.Revisions.Get(fileID, revisionID).
		Context(ctx).
		Download()
	if err != nil {
		return 0, fmt.Errorf("unable to download revision: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		return written, fmt.Errorf("unable to write revision content: %w", err)
	}

	return written, nil
}

// PartialDownloadRevision downloads a specific byte range of a file revision.
// The revision must be marked as "Keep Forever" to be downloadable.
// Returns the number of bytes written and an error if the operation fails.
func (dc *DriveClient) PartialDownloadRevision(ctx context.Context, fileID, revisionID string, w io.Writer, opts PartialDownloadOptions) (int64, error) {
	if fileID == "" {
		return 0, errors.New("file ID cannot be empty")
	}
	if revisionID == "" {
		return 0, errors.New("revision ID cannot be empty")
	}
	if opts.StartByte < 0 || opts.EndByte < 0 {
		return 0, errors.New("byte positions cannot be negative")
	}
	if opts.StartByte > opts.EndByte {
		return 0, errors.New("start byte must be less than or equal to end byte")
	}

	call := dc.service.Revisions.Get(fileID, revisionID).Context(ctx)

	// Set the Range header for partial download
	rangeHeader := fmt.Sprintf("bytes=%d-%d", opts.StartByte, opts.EndByte)
	call.Header().Set("Range", rangeHeader)

	resp, err := call.Download()
	if err != nil {
		return 0, fmt.Errorf("unable to download revision: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		return written, fmt.Errorf("unable to write revision content: %w", err)
	}

	return written, nil
}

// IsWorkspaceDocument checks if a file is a Google Workspace document.
// Returns true if the file is a Google Doc, Sheet, Slide, etc.
func (dc *DriveClient) IsWorkspaceDocument(ctx context.Context, fileID string) (bool, error) {
	if fileID == "" {
		return false, errors.New("file ID cannot be empty")
	}

	file, err := dc.service.Files.Get(fileID).
		Context(ctx).
		Fields("mimeType").
		Do()
	if err != nil {
		return false, fmt.Errorf("unable to get file metadata: %w", err)
	}

	// Google Workspace MIME types start with "application/vnd.google-apps."
	isWorkspace := len(file.MimeType) > 28 && file.MimeType[:28] == "application/vnd.google-apps."

	// Exclude folders
	if file.MimeType == "application/vnd.google-apps.folder" {
		return false, nil
	}

	return isWorkspace, nil
}
