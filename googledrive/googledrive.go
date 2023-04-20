package googledrive

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/abiiranathan/gdrive/files"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type Compession int

const (
	NoCompression              = -1
	GZipCompression Compession = iota
	ZipCompression
)

type GoogleDriveService struct {
	service     *drive.Service
	client      *http.Client
	compression Compession
}

// Initialize a new GoogleDriveService.
// If compression is provided, every file is compressed before being sent.
func NewGoogleDriveService(client *http.Client, compression ...Compession) *GoogleDriveService {
	service, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Failed to create Drive API client: %v\n", err)
	}

	svc := &GoogleDriveService{
		service:     service,
		client:      client,
		compression: NoCompression,
	}

	// Configure the compression algorithm
	if len(compression) > 0 && compression[0] >= NoCompression && compression[0] <= ZipCompression {
		svc.compression = compression[0]
	}

	return svc
}

// Uploads all files in directory syncronously.
// All intermediate directories are created.
func (svc *GoogleDriveService) UploadDirectory(dirname string, drivePath string) (driveFiles []*drive.File, err error) {
	filePaths, err := files.GetAllFiles(dirname)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %s - %v", dirname, err)
	}

	fmt.Println("Uploading directory: ", dirname)
	if svc.compression == NoCompression {
		for _, fileName := range filePaths {
			fileInfo, err := os.Stat(fileName)
			if err != nil {
				return nil, fmt.Errorf("failed to get file info: %s -  %v", fileName, err)
			}

			relativePath := files.GetRelativePath(fileName, dirname)
			// Create intermediate directories
			relativePathDir := filepath.Dir(relativePath)
			_, parentId, err := svc.MkdirAll(relativePathDir, drivePath)
			if err != nil {
				return nil, fmt.Errorf("failed to create directory: %s -  %v", relativePathDir, err)
			}

			// Create relative directory name in google drive
			driveFile, err := svc.UploadFile(fileName, fileInfo, parentId)
			if err != nil {
				return nil, err
			}

			driveFiles = append(driveFiles, driveFile)
		}
		return driveFiles, nil
	} else {
		// if dirname ends in / we get .tar.gz or .zip as the name, wierd??
		dirname = filepath.Base(dirname)
		archiveFilename, err := svc.CreateDirArchive(dirname, filePaths)
		if err != nil {
			return nil, fmt.Errorf("error compressing file: %v", err)
		}
		defer os.Remove(archiveFilename)

		fileInfo, err := os.Stat(archiveFilename)
		if err != nil {
			return nil, err
		}
		driveFile, err := svc.UploadFile(archiveFilename, fileInfo, drivePath)
		return []*drive.File{driveFile}, err
	}
}

func (svc *GoogleDriveService) UploadFile(localPath string, fileInfo fs.FileInfo, drivePath string) (*drive.File, error) {
	// Closure to perform the actual upload
	uploadFunc := func(localPath string) (*drive.File, error) {
		// Upload the file Drive
		file, err := os.Open(localPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open local file: %v", err)
		}
		defer file.Close()

		return GetOrCreateFile(context.Background(), svc, fileInfo.Name(), file, drivePath)
	}

	if svc.compression == NoCompression {
		return uploadFunc(localPath)
	} else {
		archiveFilename, err := svc.CreateFileArchive(localPath)
		if err != nil {
			return nil, fmt.Errorf("error compressing file: %v", err)
		}
		defer os.Remove(archiveFilename)
		return uploadFunc(archiveFilename)
	}
}

func (svc *GoogleDriveService) CreateFileArchive(localPath string) (archive string, err error) {
	if svc.compression == GZipCompression {
		archive = tarGzipFilename(localPath)
		err = files.GZip([]string{localPath}, archive)
	} else if svc.compression == ZipCompression {
		archive = zipFilename(localPath)
		err = files.Zip([]string{localPath}, archive)
	} else if svc.compression == NoCompression {
		return localPath, nil
	} else {
		err = fmt.Errorf("unsupported compression algorithm")
	}
	return archive, err
}

func (svc *GoogleDriveService) CreateDirArchive(dirname string, paths []string) (archive string, err error) {
	if svc.compression == GZipCompression {
		archive = tarGzipFilename(dirname)
		err = files.GZip(paths, archive)
	} else if svc.compression == ZipCompression {
		archive = zipFilename(dirname)
		err = files.Zip(paths, archive)
	} else if svc.compression == NoCompression {
		return dirname, nil
	} else {
		err = fmt.Errorf("unsupported compression algorithm")
	}
	return archive, err
}

// Keep track of created directory IDS
var dirCache = make(map[string]string)

// Creates all the directories and returns the map of dir segments to Ids
// Downloads/pdfs -> map["Downloads"]="xhhhsh.......", map["pdfs"]="ufghhd...."
// dirId -> Innermost directory id.
func (svc *GoogleDriveService) MkdirAll(
	dirPath string,
	gdriveParentId string,
) (dirmap map[string]string, dirId string, err error) {

	// Split the directory path into individual directory names.
	dirNames := strings.Split(filepath.ToSlash(dirPath), "/")
	m := make(map[string]string, len(dirNames))

	// Create each directory one at a time.
	var prevDirID string
	var index = 0
	for _, dirName := range dirNames {
		if dirName == "" {
			continue
		}

		if index == 0 {
			prevDirID = gdriveParentId
		}

		// Check if dirName in already cache
		// Must save us a tone of network calls.
		if cachedDirID, ok := dirCache[dirName]; ok {
			m[dirName] = cachedDirID
			prevDirID = cachedDirID
			index++
			continue
		}

		newDir, err := GetOrCreateDirectory(context.Background(), svc.service, dirName, prevDirID)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create directory: %v", err)
		}

		// Add dir to the map
		m[dirName] = newDir.Id
		prevDirID = newDir.Id
		index++
	}
	return m, prevDirID, nil
}

// getOrCreateDirectoryID returns the ID of the directory with the given name
// under the given parent directory, creating it if necessary.
func GetOrCreateDirectory(ctx context.Context, client *drive.Service, dirName string, parentID string) (*drive.File, error) {
	// Check if the directory already exists.
	query := fmt.Sprintf("mimeType='application/vnd.google-apps.folder' and trashed=false and name='%s' and '%s' in parents", dirName, parentID)
	results, err := client.Files.List().Q(query).PageSize(1).Fields("nextPageToken, files(id)").Do()
	if err != nil {
		return nil, err
	}

	if len(results.Files) > 0 {
		// The directory already exists, return its ID.
		return results.Files[0], nil
	}

	// The directory does not exist, create it.
	newDir := &drive.File{
		Name:     dirName,
		Parents:  []string{parentID},
		MimeType: "application/vnd.google-apps.folder",
	}
	dir, err := client.Files.Create(newDir).Do()
	if err != nil {
		return nil, err
	}
	return dir, nil
}

// getOrCreateFile returns the ID of the file with the given name
// under the given parent directory, creating it if necessary.
func GetOrCreateFile(ctx context.Context, svc *GoogleDriveService, fileName string, media io.Reader, parentID string) (*drive.File, error) {
	// Check if the file already exists.
	query := fmt.Sprintf("trashed=false and name='%s' and '%s' in parents", fileName, parentID)
	results, err := svc.service.Files.List().Q(query).PageSize(1).Fields("nextPageToken, files(id)").Do()
	if err != nil {
		return nil, err
	}

	if len(results.Files) > 0 {
		// The file already exists, return its ID.
		log.Printf("%q already exists\n\n", fileName)
		return results.Files[0], nil
	}

	newFile := &drive.File{
		Name:    fileName,
		Parents: []string{parentID},
	}

	file, err := svc.service.Files.Create(newFile).Media(media).Do()
	if err != nil {
		return nil, err
	}
	return file, nil
}

func tarGzipFilename(filename string) string {
	return filename + ".tar.gz"
}

func zipFilename(filename string) string {
	return filename + ".zip"
}
