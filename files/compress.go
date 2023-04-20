package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
)

// https://www.arthurkoziel.com/writing-tar-gz-files-in-go/
func CreateGZipArchive(files []string, buf io.Writer) error {
	gw, _ := gzip.NewWriterLevel(buf, gzip.BestCompression)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Iterate over files and and add them to the tar archive
	for _, file := range files {
		err := addToTarArchive(tw, file)
		if err != nil {
			return err
		}
	}
	return nil
}

// https://www.arthurkoziel.com/writing-tar-gz-files-in-go/
func addToTarArchive(tw *tar.Writer, filename string) error {
	// Open the file which will be written into the archive
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Get FileInfo about our file providing file size, mode, etc.
	info, err := file.Stat()
	if err != nil {
		return err
	}

	// Create a tar Header from the FileInfo data
	header, err := tar.FileInfoHeader(info, info.Name())
	if err != nil {
		return err
	}

	// Use full path as name (FileInfoHeader only takes the basename)
	// If we don't do this the directory strucuture would
	// not be preserved
	// https://golang.org/src/archive/tar/common.go?#L626
	header.Name = filename

	// Write file header to the tar archive
	err = tw.WriteHeader(header)
	if err != nil {
		return err
	}

	// Copy file content to tar archive
	_, err = io.Copy(tw, file)
	if err != nil {
		return err
	}
	return nil
}

// Create .tar.gz archive for files. Write output to outputZipFilename.
func GZip(files []string, outputTarFilename string) error {
	out, err := os.Create(outputTarFilename)
	if err != nil {
		return fmt.Errorf("error writing archive: %w", err)
	}
	defer out.Close()
	return CreateGZipArchive(files, out)
}

// Create .zip archive for files. Write output to outputZipFilename.
func Zip(files []string, outputZipFilename string) error {
	out, err := os.Create(outputZipFilename)
	if err != nil {
		return fmt.Errorf("error writing zip file: %w", err)
	}
	defer out.Close()
	return CreateZipArchive(files, out)
}

func CreateZipArchive(files []string, buf io.Writer) error {
	zipWriter := zip.NewWriter(buf)
	defer zipWriter.Close()

	// Iterate over files and and add them to the tar archive
	for _, file := range files {
		err := addToZipArchive(zipWriter, file)
		if err != nil {
			return err
		}
	}
	return nil
}

func addToZipArchive(zipWriter *zip.Writer, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Get FileInfo about our file providing file size, mode, etc.
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(fileInfo)
	if err != nil {
		return err
	}

	// Set the name of the file within the ZIP archive to be the same as the original file
	header.Name = fileInfo.Name()

	// Create a new file within the ZIP archive with the same name as the original file
	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, file)
	if err != nil {
		return err
	}
	return zipWriter.Flush()
}
