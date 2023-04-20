package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
)

// GZipDecompress decompresses a .tar.gz file to a specified output directory
func GZipDecompress(filePath, outputDir string) error {
	// Open the compressed file for reading
	compressedFile, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer compressedFile.Close()

	// Create a gzip reader to read the compressed data
	gzipReader, err := gzip.NewReader(compressedFile)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	// Create a tar reader to read the uncompressed data
	tarReader := tar.NewReader(gzipReader)

	// Loop through each file in the archive
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			// End of archive
			break
		} else if err != nil {
			return err
		}

		// Determine the full path of the file
		filePath := filepath.Join(outputDir, header.Name)

		// Check if the file is a directory
		if header.Typeflag == tar.TypeDir {
			// Create the directory if it doesn't already exist
			err := os.MkdirAll(filePath, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			continue
		}

		// Create the file
		file, err := os.Create(filePath)
		if err != nil {
			return err
		}

		// Write the file data
		_, err = io.Copy(file, tarReader)
		if err != nil {
			file.Close()
			return err
		}

		// Close the file
		file.Close()

		// Set the file permissions
		err = os.Chmod(filePath, os.FileMode(header.Mode))
		if err != nil {
			return err
		}
	}
	return nil
}

func UnZip(zipFileName string, destDir string) error {
	// Open the zip archive file
	zipFile, err := zip.OpenReader(zipFileName)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	// Extract each file in the archive
	for _, file := range zipFile.File {
		filePath := filepath.Join(destDir, file.Name)

		if file.FileInfo().IsDir() {
			// Create the directory if it doesn't exist
			if err := os.MkdirAll(filePath, file.Mode()); err != nil {
				return err
			}
			continue
		}

		// Create the file to write to
		fileToExtract, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}
		defer fileToExtract.Close()

		// Open the file in the archive
		fileInArchive, err := file.Open()
		if err != nil {
			return err
		}
		defer fileInArchive.Close()

		// Copy the file contents to the destination file
		_, err = io.Copy(fileToExtract, fileInArchive)
		if err != nil {
			return err
		}
	}
	return nil
}
