package bgm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var audioExtensions = map[string]struct{}{
	".mp3":  {},
	".wav":  {},
	".flac": {},
	".m4a":  {},
	".aac":  {},
	".ogg":  {},
	".opus": {},
}

// ScannedFile is a music library file with the metadata used for cache
// invalidation.
type ScannedFile struct {
	Path     string
	Filename string
	Size     int64
	MtimeNS  int64
}

// ScanDir lists supported audio files at the top level of dir. It returns the
// absolute library directory so cache entries use stable paths.
func ScanDir(dir string) (string, []ScannedFile, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve BGM directory %s: %w", dir, err)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return "", nil, fmt.Errorf("read BGM directory %s: %w", absDir, err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("%s is not a directory", absDir)
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", nil, fmt.Errorf("read BGM directory %s: %w", absDir, err)
	}

	files := make([]ScannedFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, ok := audioExtensions[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			continue
		}
		path := filepath.Join(absDir, entry.Name())
		stat, err := os.Stat(path)
		if err != nil {
			return "", nil, fmt.Errorf("stat BGM file %s: %w", path, err)
		}
		if !stat.Mode().IsRegular() {
			continue
		}
		files = append(files, ScannedFile{
			Path:     path,
			Filename: entry.Name(),
			Size:     stat.Size(),
			MtimeNS:  stat.ModTime().UnixNano(),
		})
	}
	return absDir, files, nil
}
