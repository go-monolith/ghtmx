package testwatch

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd/modcheck"
	"github.com/go-monolith/ghtmx/internal/htmlfind"
	"golang.org/x/net/html"
	"golang.org/x/sync/errgroup"
)

//go:embed testdata/*
var testdata embed.FS

func createTestProject(moduleRoot string) (dir string, err error) {
	dir, err = os.MkdirTemp("", "ghtmx_watch_test_*")
	if err != nil {
		return dir, fmt.Errorf("failed to make test dir: %w", err)
	}
	files, err := testdata.ReadDir("testdata")
	if err != nil {
		return dir, fmt.Errorf("failed to read embedded dir: %w", err)
	}
	for _, file := range files {
		src := filepath.Join("testdata", file.Name())
		data, err := testdata.ReadFile(src)
		if err != nil {
			return dir, fmt.Errorf("failed to read file: %w", err)
		}

		target := filepath.Join(dir, file.Name())
		if file.Name() == "go.mod.embed" {
			data = bytes.ReplaceAll(data, []byte("{moduleRoot}"), []byte(moduleRoot))
			target = filepath.Join(dir, "go.mod")
		}
		err = os.WriteFile(target, data, 0660)
		if err != nil {
			return dir, fmt.Errorf("failed to copy file: %w", err)
		}
	}
	return dir, nil
}

func replaceInFile(name, src, tgt string) error {
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	updated := strings.ReplaceAll(string(data), src, tgt)
	return os.WriteFile(name, []byte(updated), 0660)
}

func getPort() (port int, err error) {
	var a *net.TCPAddr
	a, err = net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, fmt.Errorf("failed to resolve TCP address: %w", err)
	}
	l, err := net.ListenTCP("tcp", a)
	if err != nil {
		return 0, fmt.Errorf("failed to listen on TCP: %w", err)
	}
	return l.Addr().(*net.TCPAddr).Port, l.Close()
}

func getHTML(url string) (n *html.Node, err error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get %q: %w", url, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	return html.Parse(resp.Body)
}

func TestCanAccessDirect(t *testing.T) {
	if testing.Short() {
		return
	}
	args, teardown, err := Setup(false)
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)

	// Assert.
	doc, err := getHTML(args.AppURL)
	if err != nil {
		t.Fatalf("failed to read HTML: %v", err)
	}
	countElements := htmlfind.All(doc, htmlfind.Element("div", htmlfind.Attr("data-testid", "count")))
	if len(countElements) != 1 {
		t.Fatalf("expected 1 count element, got %d", len(countElements))
	}
	countText := countElements[0].FirstChild.Data
	actualCount, err := strconv.Atoi(countText)
	if err != nil {
		t.Fatalf("got count %q instead of integer", countText)
	}
	if actualCount < 1 {
		t.Errorf("expected count >= 1, got %d", actualCount)
	}
}

func TestCanAccessViaProxy(t *testing.T) {
	if testing.Short() {
		return
	}
	args, teardown, err := Setup(false)
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)

	// Assert.
	doc, err := getHTML(args.ProxyURL)
	if err != nil {
		t.Fatalf("failed to read HTML: %v", err)
	}
	countElements := htmlfind.All(doc, htmlfind.Element("div", htmlfind.Attr("data-testid", "count")))
	if len(countElements) != 1 {
		t.Fatalf("expected 1 count element, got %d", len(countElements))
	}
	countText := countElements[0].FirstChild.Data
	actualCount, err := strconv.Atoi(countText)
	if err != nil {
		t.Fatalf("got count %q instead of integer", countText)
	}
	if actualCount < 1 {
		t.Errorf("expected count >= 1, got %d", actualCount)
	}
}

type Event struct {
	Type string
	Data string
}

func readSSE(ctx context.Context, url string, sse chan<- Event) (err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	var e Event
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			sse <- e
			e = Event{}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			e.Type = line[len("event: "):]
		}
		if strings.HasPrefix(line, "data: ") {
			e.Data = line[len("data: "):]
		}
	}
	return scanner.Err()
}

func TestFileModificationsResultInSSEWithGzip(t *testing.T) {
	if testing.Short() {
		return
	}
	args, teardown, err := Setup(false)
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)

	// Start the SSE check.
	events := make(chan Event)
	var eventsErr error
	go func() {
		eventsErr = readSSE(context.Background(), fmt.Sprintf("%s/_ghtmx/reload/events", args.ProxyURL), events)
	}()

	// Assert data is expected.
	doc, err := getHTML(args.ProxyURL)
	if err != nil {
		t.Fatalf("failed to read HTML: %v", err)
	}
	modified := htmlfind.All(doc, htmlfind.Element("div", htmlfind.Attr("data-testid", "modification")))
	if len(modified) != 1 {
		t.Fatalf("expected 1 modification element, got %d", len(modified))
	}
	if text := modified[0].FirstChild.Data; text != "Original" {
		t.Errorf("expected %q, got %q", "Original", text)
	}

	// Change file.
	templFile := filepath.Join(args.AppDir, "templates.ghtmx")
	err = replaceInFile(templFile,
		`<div data-testid="modification">Original</div>`,
		`<div data-testid="modification">Updated</div>`)
	if err != nil {
		t.Errorf("failed to replace text in file: %v", err)
	}

	// Give the filesystem watcher a few seconds.
	var reloadCount int
loop:
	for {
		select {
		case event := <-events:
			if event.Data == "reload" {
				reloadCount++
				break loop
			}
		case <-time.After(time.Second * 5):
			break loop
		}
	}
	if reloadCount == 0 {
		t.Error("failed to receive SSE about update after 5 seconds")
	}

	// Check to see if there were any errors.
	if eventsErr != nil {
		t.Errorf("error reading events: %v", err)
	}

	// See results in browser immediately.
	doc, err = getHTML(args.ProxyURL)
	if err != nil {
		t.Fatalf("failed to read HTML: %v", err)
	}
	modified = htmlfind.All(doc, htmlfind.Element("div", htmlfind.Attr("data-testid", "modification")))
	if len(modified) != 1 {
		t.Fatalf("expected 1 modification element, got %d", len(modified))
	}
	if text := modified[0].FirstChild.Data; text != "Updated" {
		t.Errorf("expected %q, got %q", "Updated", text)
	}
}

func TestFileModificationsResultInSSE(t *testing.T) {
	if testing.Short() {
		return
	}
	args, teardown, err := Setup(false)
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)

	// Start the SSE check.
	events := make(chan Event)
	var eventsErr error
	go func() {
		eventsErr = readSSE(context.Background(), fmt.Sprintf("%s/_ghtmx/reload/events", args.ProxyURL), events)
	}()

	// Assert data is expected.
	doc, err := getHTML(args.ProxyURL)
	if err != nil {
		t.Fatalf("failed to read HTML: %v", err)
	}
	modified := htmlfind.All(doc, htmlfind.Element("div", htmlfind.Attr("data-testid", "modification")))
	if len(modified) != 1 {
		t.Fatalf("expected 1 modification element, got %d", len(modified))
	}
	if text := modified[0].FirstChild.Data; text != "Original" {
		t.Errorf("expected %q, got %q", "Original", text)
	}

	// Change file.
	templFile := filepath.Join(args.AppDir, "templates.ghtmx")
	err = replaceInFile(templFile,
		`<div data-testid="modification">Original</div>`,
		`<div data-testid="modification">Updated</div>`)
	if err != nil {
		t.Errorf("failed to replace text in file: %v", err)
	}

	// Give the filesystem watcher a few seconds.
	var reloadCount int
loop:
	for {
		select {
		case event := <-events:
			if event.Data == "reload" {
				reloadCount++
				break loop
			}
		case <-time.After(time.Second * 5):
			break loop
		}
	}
	if reloadCount == 0 {
		t.Error("failed to receive SSE about update after 5 seconds")
	}

	// Check to see if there were any errors.
	if eventsErr != nil {
		t.Errorf("error reading events: %v", err)
	}

	// See results in browser immediately.
	doc, err = getHTML(args.ProxyURL)
	if err != nil {
		t.Fatalf("failed to read HTML: %v", err)
	}
	modified = htmlfind.All(doc, htmlfind.Element("div", htmlfind.Attr("data-testid", "modification")))
	if len(modified) != 1 {
		t.Fatalf("expected 1 modification element, got %d", len(modified))
	}
	if text := modified[0].FirstChild.Data; text != "Updated" {
		t.Errorf("expected %q, got %q", "Updated", text)
	}
}

func NewTestArgs(modRoot, appDir string, appPort int, proxyBind string, proxyPort int) TestArgs {
	return TestArgs{
		ModRoot:   modRoot,
		AppDir:    appDir,
		AppPort:   appPort,
		AppURL:    fmt.Sprintf("http://localhost:%d", appPort),
		ProxyBind: proxyBind,
		ProxyPort: proxyPort,
		ProxyURL:  fmt.Sprintf("http://%s:%d", proxyBind, proxyPort),
	}
}

type TestArgs struct {
	ModRoot   string
	AppDir    string
	AppPort   int
	AppURL    string
	ProxyBind string
	ProxyPort int
	ProxyURL  string
}

func Setup(gzipEncoding bool) (args TestArgs, teardown func(t *testing.T), err error) {
	return setupWithLog(gzipEncoding, io.Discard, false)
}

// setupWithLog also captures the generate command's log output and,
// when allowRunError is set, tolerates a non-nil Run result — a watch
// session that saw error-level diagnostics reports them on exit.
func setupWithLog(gzipEncoding bool, logW io.Writer, allowRunError bool) (args TestArgs, teardown func(t *testing.T), err error) {
	return setupWithLogLevel(gzipEncoding, logW, allowRunError, "info")
}

func setupWithLogLevel(gzipEncoding bool, logW io.Writer, allowRunError bool, logLevel string) (args TestArgs, teardown func(t *testing.T), err error) {
	wd, err := os.Getwd()
	if err != nil {
		return args, teardown, fmt.Errorf("could not find working dir: %w", err)
	}
	moduleRoot, err := modcheck.WalkUp(wd)
	if err != nil {
		return args, teardown, fmt.Errorf("could not find local templ go.mod file: %v", err)
	}

	appDir, err := createTestProject(moduleRoot)
	if err != nil {
		return args, teardown, fmt.Errorf("failed to create test project: %v", err)
	}
	appPort, err := getPort()
	if err != nil {
		return args, teardown, fmt.Errorf("failed to get available port: %v", err)
	}
	proxyPort, err := getPort()
	if err != nil {
		return args, teardown, fmt.Errorf("failed to get available port: %v", err)
	}
	proxyBind := "localhost"

	args = NewTestArgs(moduleRoot, appDir, appPort, proxyBind, proxyPort)
	ctx, cancel := context.WithCancel(context.Background())

	var wg errgroup.Group
	wg.Go(func() error {
		command := fmt.Sprintf("go run . -port %d", args.AppPort)
		if gzipEncoding {
			command += " -gzip true"
		}
		return generatecmd.Run(ctx, io.Discard, logW, []string{"-path", appDir, "-watch", "-log-level", logLevel, "-proxybind", proxyBind, "-proxyport", strconv.Itoa(proxyPort), "-proxy", args.AppURL, "-open-browser=false", "-cmd", command})
	})

	// Wait for server to start.
	if err = waitForURL(args.AppURL); err != nil {
		cancel()
		cmdErr := wg.Wait()
		return args, teardown, fmt.Errorf("failed to start app server: %w", errors.Join(cmdErr, err))
	}
	if err = waitForURL(args.ProxyURL); err != nil {
		cancel()
		cmdErr := wg.Wait()
		return args, teardown, fmt.Errorf("failed to start proxy server: %w", errors.Join(cmdErr, err))
	}

	// Wait for exit.
	teardown = func(t *testing.T) {
		cancel()
		if cmdErr := wg.Wait(); cmdErr != nil && !allowRunError {
			t.Errorf("failed to run generate cmd: %v", cmdErr)
		}
		if err = os.RemoveAll(appDir); err != nil {
			t.Fatalf("failed to remove test dir %q: %v", appDir, err)
		}
	}
	return args, teardown, err
}

func waitForURL(url string) (err error) {
	var tries int
	for {
		time.Sleep(time.Second)
		if tries > 20 {
			return err
		}
		tries++
		var resp *http.Response
		resp, err = http.Get(url)
		if err != nil {
			fmt.Printf("failed to get %q: %v\n", url, err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			fmt.Printf("failed to get %q: %v\n", url, err)
			err = fmt.Errorf("expected status code %d, got %d", http.StatusOK, resp.StatusCode)
			continue
		}
		return nil
	}
}

func TestGenerateReturnsErrors(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not find working dir: %v", err)
	}
	moduleRoot, err := modcheck.WalkUp(wd)
	if err != nil {
		t.Fatalf("could not find local templ go.mod file: %v", err)
	}

	appDir, err := createTestProject(moduleRoot)
	if err != nil {
		t.Fatalf("failed to create test project: %v", err)
	}
	defer func() {
		if err = os.RemoveAll(appDir); err != nil {
			t.Fatalf("failed to remove test dir %q: %v", appDir, err)
		}
	}()

	// Break the HTML.
	templFile := filepath.Join(appDir, "templates.ghtmx")
	err = replaceInFile(templFile,
		`<div data-testid="modification">Original</div>`,
		`<div data-testid="modification" -unclosed div-</div>`)
	if err != nil {
		t.Errorf("failed to replace text in file: %v", err)
	}

	// Run.
	err = generatecmd.Run(context.Background(), io.Discard, io.Discard, []string{"-path", appDir, "-include-version=false", "-include-timestamp=false", "-keep-orphaned-files=false"})
	if err == nil {
		t.Errorf("expected generation error, got %v", err)
	}
}

// TestFailedRegenerationDoesNotReload: FR-063 — a change producing
// error-level diagnostics sends no reload event and leaves the previous
// good build serving; fixing the error reloads.
func TestFailedRegenerationDoesNotReload(t *testing.T) {
	if testing.Short() {
		return
	}
	log := &syncBuffer{}
	args, teardown, err := setupWithLogLevel(false, log, true, "debug")
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)

	events := make(chan Event, 64)
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	go func() {
		_ = readSSE(sseCtx, fmt.Sprintf("%s/_ghtmx/reload/events", args.ProxyURL), events)
	}()

	doc, err := getHTML(args.ProxyURL)
	if err != nil {
		t.Fatalf("failed to read HTML: %v", err)
	}
	if got := htmlfind.All(doc, htmlfind.Element("div", htmlfind.Attr("data-testid", "modification"))); len(got) != 1 || got[0].FirstChild.Data != "Original" {
		t.Fatalf("baseline page must serve Original")
	}

	// Break the template: an unknown hx-* attribute is an error-level
	// diagnostic (GHTMX-E0201), so regeneration fails.
	templFile := filepath.Join(args.AppDir, "templates.ghtmx")
	if err := replaceInFile(templFile,
		`<div data-testid="modification">Original</div>`,
		`<div data-testid="modification" hx-pots="/x">Broken</div>`); err != nil {
		t.Fatal(err)
	}
	// Wait until the watcher actually processed the failure.
	failureSeen := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if strings.Contains(log.String(), "GHTMX-E0201") {
			failureSeen = true
			break
		}
	}
	if !failureSeen {
		t.Fatalf("the failure diagnostic never surfaced:\n%s", log.String())
	}
	// The failing batch must close and report the suppression on its own.
	batchClosed := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if strings.Contains(log.String(), "Skipping reload") {
			batchClosed = true
			break
		}
	}
	if !batchClosed {
		t.Fatalf("the failing batch never closed:\n%s", log.String())
	}

	// No reload may arrive while the error stands; pings and other events
	// are drained.
	noReload := time.After(3 * time.Second)
window:
	for {
		select {
		case event := <-events:
			if event.Data == "reload" {
				t.Error("a failed regeneration must not reload the browser")
			}
		case <-noReload:
			break window
		}
	}

	// The previous good build keeps serving.
	doc, err = getHTML(args.ProxyURL)
	if err != nil {
		t.Fatalf("failed to read HTML: %v", err)
	}
	if got := htmlfind.All(doc, htmlfind.Element("div", htmlfind.Attr("data-testid", "modification"))); len(got) != 1 || got[0].FirstChild.Data != "Original" {
		t.Error("the previous good build must keep serving while the error stands")
	}

	// Fix the template: the next regeneration succeeds and reloads.
	if err := replaceInFile(templFile,
		`<div data-testid="modification" hx-pots="/x">Broken</div>`,
		`<div data-testid="modification">Fixed</div>`); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Data == "reload" {
				return // Reload after the fix: FR-063 satisfied.
			}
		case <-deadline:
			t.Fatalf("no reload after the error was fixed; log:\n%s", log.String())
		}
	}
}

// syncBuffer is a goroutine-safe log capture.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
