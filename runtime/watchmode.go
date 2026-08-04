package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var developmentMode = os.Getenv("GHTMX_DEV_MODE") == "true"

var stringLoaderOnce = sync.OnceValue(func() *StringLoader {
	return NewStringLoader(os.Getenv("GHTMX_DEV_MODE_WATCH_ROOT"))
})

// WriteString writes the string to the writer. If development mode is enabled
// s is replaced with the string at the index in the _ghtmx.txt file.
func WriteString(w io.Writer, index int, s string) (err error) {
	if developmentMode {
		_, path, _, _ := runtime.Caller(1)
		if !strings.HasSuffix(path, "_ghtmx.go") {
			return errors.New("ghtmx: attempt to use WriteString from a non ghtmx file")
		}
		s, err = stringLoaderOnce().GetWatchedString(path, index, s)
		if err != nil {
			return fmt.Errorf("ghtmx: failed to get watched string: %w", err)
		}
	}
	_, err = io.WriteString(w, s)
	return err
}

// TemplateExtensions are the extensions a project may write templates
// with, most canonical first. It mirrors config.TemplateExtensions, which
// this package cannot import: NFR-012 keeps everything an application
// links standard-library only. TestRuntimeTemplateExtensionsMatchConfig
// fails if the two drift.
var TemplateExtensions = []string{".ghtmx", ".htmx"}

// GetDevModeTextFileName returns the sidecar path holding a template's
// string literals, keyed by a hash of the template's own path.
//
// Both sides of dev-mode hot reload must agree on that path. The writer
// (the generator) knows the real template name; the reader is generated
// code that knows only its own _ghtmx.go path, so it has to recover the
// template name from it. Appending the canonical extension unconditionally
// made a .htmx project hash two different paths on the two sides, and hot
// reload simply stopped finding its file.
//
// The extension is project configuration this package cannot read, so the
// file on disk is the signal: dev mode runs against the source tree by
// construction. With no template found the canonical extension still
// applies, which is exactly the previous behaviour.
func GetDevModeTextFileName(templFileName string) string {
	if prefix, ok := strings.CutSuffix(templFileName, "_ghtmx.go"); ok {
		templFileName = prefix + templateExtensionFor(prefix)
	}
	absFileName, err := filepath.Abs(templFileName)
	if err != nil {
		absFileName = templFileName
	}
	absFileName, err = filepath.EvalSymlinks(absFileName)
	if err != nil {
		absFileName = templFileName
	}
	absFileName = normalizePath(absFileName)

	hashedFileName := sha256.Sum256([]byte(absFileName))
	outputFileName := fmt.Sprintf("ghtmx_%s.txt", hex.EncodeToString(hashedFileName[:]))

	root := os.TempDir()
	if os.Getenv("GHTMX_DEV_MODE_ROOT") != "" {
		root = os.Getenv("GHTMX_DEV_MODE_ROOT")
	}

	return filepath.Join(root, outputFileName)
}

// templateExtensionFor picks the extension whose template actually sits
// beside the generated file. Order matters only when a stale file of the
// other extension lingers; the canonical one wins.
func templateExtensionFor(prefix string) string {
	for _, ext := range TemplateExtensions {
		if _, err := os.Stat(prefix + ext); err == nil {
			return ext
		}
	}
	return TemplateExtensions[0]
}

// normalizePath converts Windows paths to Unix style paths.
func normalizePath(p string) string {
	p = strings.ReplaceAll(filepath.Clean(p), `\`, `/`)
	parts := strings.SplitN(p, ":", 2)
	if len(parts) == 2 && len(parts[0]) == 1 {
		drive := strings.ToLower(parts[0])
		p = "/" + drive + parts[1]
	}
	return p
}

type watchState struct {
	modTime time.Time
	strings []string
}

type StringLoader struct {
	watchModeRoot    string
	watchModeRootErr error
	cache            map[string]watchState
	cacheMutex       sync.Mutex
}

func NewStringLoader(devModeWatchRootPath string) (sl *StringLoader) {
	sl = &StringLoader{
		cache: make(map[string]watchState),
	}
	if devModeWatchRootPath == "" {
		return sl
	}
	resolvedRoot, err := filepath.EvalSymlinks(devModeWatchRootPath)
	if err != nil {
		sl.watchModeRootErr = fmt.Errorf("ghtmx: failed to eval symlinks for watch mode root %q: %w", devModeWatchRootPath, err)
		return sl
	}
	sl.watchModeRoot = resolvedRoot
	return sl
}

func (sl *StringLoader) GetWatchedString(templFilePath string, index int, defaultValue string) (string, error) {
	if sl.watchModeRootErr != nil {
		return "", sl.watchModeRootErr
	}
	path, err := filepath.EvalSymlinks(templFilePath)
	if err != nil {
		return "", fmt.Errorf("ghtmx: failed to eval symlinks for %q: %w", path, err)
	}
	// If the file is outside the watch mode root, write the string directly.
	// If watch mode root is not set, fall back to the previous behaviour to avoid breaking existing setups.
	if sl.watchModeRoot != "" && !strings.HasPrefix(path, sl.watchModeRoot) {
		return defaultValue, nil
	}

	txtFilePath := GetDevModeTextFileName(path)
	literals, err := sl.getWatchedStrings(txtFilePath)
	if err != nil {
		return "", fmt.Errorf("ghtmx: failed to get watched strings for %q: %w", path, err)
	}
	if index > len(literals) {
		return "", fmt.Errorf("ghtmx: failed to find line %d in %s", index, txtFilePath)
	}
	return strconv.Unquote(`"` + literals[index-1] + `"`)
}

func (sl *StringLoader) getWatchedStrings(txtFilePath string) ([]string, error) {
	sl.cacheMutex.Lock()
	defer sl.cacheMutex.Unlock()

	state, cached := sl.cache[txtFilePath]
	if !cached {
		return sl.cacheStrings(txtFilePath)
	}

	if time.Since(state.modTime) < time.Millisecond*100 {
		return state.strings, nil
	}

	info, err := os.Stat(txtFilePath)
	if err != nil {
		return nil, fmt.Errorf("ghtmx: failed to stat %s: %w", txtFilePath, err)
	}

	if !info.ModTime().After(state.modTime) {
		return state.strings, nil
	}

	return sl.cacheStrings(txtFilePath)
}

func (sl *StringLoader) cacheStrings(txtFilePath string) ([]string, error) {
	txtFile, err := os.Open(txtFilePath)
	if err != nil {
		return nil, fmt.Errorf("ghtmx: failed to open %s: %w", txtFilePath, err)
	}
	defer func() {
		_ = txtFile.Close()
	}()

	info, err := txtFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("ghtmx: failed to stat %s: %w", txtFilePath, err)
	}

	all, err := io.ReadAll(txtFile)
	if err != nil {
		return nil, fmt.Errorf("ghtmx: failed to read %s: %w", txtFilePath, err)
	}

	literals := strings.Split(string(all), "\n")
	sl.cache[txtFilePath] = watchState{
		modTime: info.ModTime(),
		strings: literals,
	}

	return literals, nil
}
