# gdrive


[![GitHub license](https://img.shields.io/github/license/abiiranathan/gdrive)](https://github.com/abiiranathan/gdrive/blob/main/LICENSE)
[![GoDoc](https://godoc.org/github.com/abiiranathan/gdrive?status.svg)](https://godoc.org/github.com/abiiranathan/gdrive)

*gdrive* is a command-line interface (CLI) and set of APIs for uploading files and folders to Google Drive. This project makes use of the Google Drive API to perform the upload operation. It's written in the Go programming language.

## Features
- Upload files and folders from local machine to Google Drive.
- Supports gzip compression as .tar.gz on files before upload.
- Supports ZIP compression as .zip on files before upload.
- Upload multiple files and folders simultaneously.
- Authentication with Google Drive API using API credentials file and token file.
- Automatic access_token capture and renewal.
- Skip existing files.
- Automatically create intermediate folders if missing.
  
## Prerequisites
Before using this CLI, you must:

- Enable the Google Drive API: 
   https://console.cloud.google.com/flows/enableapi?apiid=drive.googleapis.com

- Create API credentials.json: 
	https://console.cloud.google.com/apis/credentials


### Installation of the CLI:
```bash
go install github.com/abiiranathan/gdrive@latest
```

### Installation of the client APIs
```
go get github.com/abiiranathan/gdrive
```

See [usage example](./main.go) on how to use the APIs.

### Usage:

Run `gdrive --help` to get detailed usage instructions.

---
### Download pre-compiled Linux x86_64(64 bit) binary or a windows executable.(64 bit)
If you want to hack on something quickly, download pre-compiled version from the [Releases](http://github.com/abiiranathan/gdrive/releases)
