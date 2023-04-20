/*
Google Drive Uploader
Command line interface for uploading file and folders to google drive.

Enable the Google Drive API: https://console.cloud.google.com/flows/enableapi?apiid=drive.googleapis.com

Create API credentials.json: https://console.cloud.google.com/apis/credentials
*/
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/abiiranathan/gdrive/auth"
	"github.com/abiiranathan/gdrive/googledrive"
	"github.com/abiiranathan/gdrive/server"
)

var UsageText = `

Usage: gdrive [OPTIONS] <drive_path_id> <local_path_1> [<local_path_2> ...]

Description:
   This program uploads files from local machine to google drive.

Arguments:
   <drive_path_id>       The ID of the drive path to upload files to.
   <local_path_1>        The local path to file or directory to upload.
   [<local_path_2> ...]  Optional additional local paths to be uploaded.

Options:
   -creds <file>        Path to Google API credentials file. Default: credentials.json
   -token <file>        Path to Google Drive token file. Default: token.json
   -port <port>         Server port for handling access tokens. Default: 8888
   -gzip <bool>         Perform gzip compression as .tar.gz on files before upload.
   -zip <bool>          Perform gzip compression as .zip on files before upload.
   -h, --help           Show this help message and exit.

Example:
   gdrive -creds /path/to/creds.json -token /path/to/token.json -port 8080 1pwmMXssnt1I5AORDJcNkHWVeVurTacx15 /home/user/Documents
   gdrive 1pwmMXssnt1I5AORDJcNkHWVeVurTacx15 /home/user/Documents /home/user/Pictures


Enable the Google Drive API: 
   https://console.cloud.google.com/flows/enableapi?apiid=drive.googleapis.com

Create API credentials.json: 
	https://console.cloud.google.com/apis/credentials
`

var (
	credentialsFile string
	tokenFile       string
	RedirectURL     string
	Port            string
	GZipCompress    bool
	ZipCompress     bool
)

var showHelp = flag.Bool("help", false, "Show this help message and exit.")

func init() {
	flag.StringVar(&credentialsFile, "creds", "credentials.json", "Path to Google API credentials file")
	flag.StringVar(&tokenFile, "token", "token.json", "Path to Google Drive token file")
	flag.StringVar(&Port, "port", "8888", "Server Port for handling access tokens")
	flag.BoolVar(&GZipCompress, "gzip", false, "Perform gzip compression as .tar.gz on file before upload")
	flag.BoolVar(&ZipCompress, "zip", false, "Perform ZIP compression as .zip on file before upload")
	flag.Parse()
}

func Usage() {
	fmt.Fprintln(flag.CommandLine.Output(), UsageText)
}

func main() {
	flag.Usage = Usage
	if *showHelp || flag.NArg() < 2 {
		flag.Usage()
		os.Exit(1)
	}

	RedirectURL = fmt.Sprintf("http://localhost:%s", Port)
	drivePathID := flag.Arg(0)

	// Capture all the paths to upload
	localPaths := []string{}
	for i := 1; i < flag.NArg(); i++ {
		path := flag.Arg(i)
		if path != "" {
			localPaths = append(localPaths, path)
		}
	}

	// Initialize the GoogleAuth service
	authSvc := auth.GoogleAuth{
		CredentialFile: credentialsFile,
		TokenFile:      tokenFile,
		RedirectURL:    RedirectURL,
		TokenServer:    server.NewTokenServer(Port),
	}

	// Authenticate with Google Drive API
	client, err := authSvc.GetClient()
	if err != nil {
		log.Fatalf("Failed to create Drive client: %v\n", err)
	}

	var compression googledrive.Compession
	if GZipCompress {
		compression = googledrive.GZipCompression
	} else if ZipCompress {
		compression = googledrive.ZipCompression
	}

	// Initialize upload service with the http client
	// Pass in the compression.
	service := googledrive.NewGoogleDriveService(client, compression)
	// Upload all the paths
	upload(service, drivePathID, localPaths...)
}

func upload(svc *googledrive.GoogleDriveService, drivePathID string, localPaths ...string) {
	for _, localPath := range localPaths {
		fi, err := os.Stat(localPath)
		if err != nil {
			log.Fatalf("Failed to get path info: %v\n", err)
		}

		if fi.IsDir() {
			svc.UploadDirectory(localPath, drivePathID)
		} else {
			svc.UploadFile(localPath, fi, drivePathID)
		}
	}
}
