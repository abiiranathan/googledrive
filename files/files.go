package files

import (
	"path/filepath"
	"strings"

	"github.com/abiiranathan/walkman"
)

// Returns all the files in the directory.
// Recurses to find all the paths in parallel using walkman module.
// skip: names of files to ignore e.g .git, .env etc
func GetAllFiles(localPath string, skip ...string) ([]string, error) {
	wm := walkman.New(walkman.NoDefaultSkip(), walkman.SkipDirs(skip))
	results, err := wm.Walk(localPath)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, file := range results.ToSlice() {
		files = append(files, file.Path)
	}
	return files, nil
}

// GetRelativePaths returns a file path with the given base directory removed and the directory
// name of the base directory added to the start of each path.
// It takes a path to a file and a base directory
// as input parameters. The function removes the base directory from each file path in the input slice,
// and removes the leading path separator if any.
// Then it joins the directory name of the base directory
// and the relative path using `filepath.Join`, and finally returns the new relative file path.
func GetRelativePath(file string, baseDir string) string {
	relativePath := strings.TrimPrefix(file, baseDir)
	// remove leading path separator, if any
	if len(relativePath) > 0 && relativePath[0] == filepath.Separator {
		relativePath = relativePath[1:]
	}
	relativePath = filepath.Join(filepath.Base(baseDir), relativePath)
	return relativePath
}
