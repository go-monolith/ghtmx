package infocmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/lspcmd/pls"
)

type Arguments struct {
	JSON bool `flag:"json" help:"Output info as JSON."`
}

type Info struct {
	OS struct {
		GOOS   string `json:"goos"`
		GOARCH string `json:"goarch"`
	} `json:"os"`
	Go    ToolInfo `json:"go"`
	Gopls ToolInfo `json:"gopls"`
	Ghtmx ToolInfo `json:"ghtmx"`
}

type ToolInfo struct {
	Location string     `json:"location"`
	Version  string     `json:"version"`
	Level    slog.Level `json:"level"`
	Message  string     `json:"message,omitempty"`
}

func getGoInfo() (d ToolInfo) {
	d.Level = slog.LevelError

	var err error
	d.Location, err = exec.LookPath("go")
	if err != nil {
		d.Message = fmt.Sprintf("failed to find go: %v", err)
		return
	}
	cmd := exec.Command(d.Location, "version")
	v, err := cmd.Output()
	if err != nil {
		d.Message = fmt.Sprintf("failed to get go version, check that Go is installed: %v", err)
		return
	}
	d.Version = strings.TrimSpace(string(v))
	d.Level = slog.LevelInfo
	return
}

func getGoplsInfo() (d ToolInfo) {
	d.Level = slog.LevelError

	var err error
	d.Location, err = pls.FindGopls()
	if err != nil {
		d.Message = fmt.Sprintf("failed to find gopls: %v", err)
		return
	}
	d.Version, err = pls.GoplsVersion(d.Location)
	if err != nil {
		d.Message = fmt.Sprintf("failed to get gopls version: %v", err)
		return
	}
	d.Level = slog.LevelInfo
	return
}

func getGhtmxInfo() (d ToolInfo) {
	d.Level = slog.LevelError

	var err error
	d.Location, err = findGhtmx()
	if err != nil {
		d.Message = err.Error()
		return
	}
	cmd := exec.Command(d.Location, "version")
	v, err := cmd.Output()
	if err != nil {
		d.Message = fmt.Sprintf("failed to get ghtmx version: %v", err)
		return
	}
	d.Version = strings.TrimSpace(string(v))
	if d.Version != ghtmx.Version() {
		d.Message = fmt.Sprintf("version mismatch - you're running %q at the command line, but the version in the path is %q", ghtmx.Version(), d.Version)
		return
	}
	d.Level = slog.LevelInfo
	return
}

func findGhtmx() (location string, err error) {
	executableName := "ghtmx"
	if runtime.GOOS == "windows" {
		executableName = "ghtmx.exe"
	}
	executableName, err = exec.LookPath(executableName)
	if err == nil {
		// Found on the path.
		return executableName, nil
	}

	// Unexpected error.
	if !errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("unexpected error looking for ghtmx: %w", err)
	}

	return "", fmt.Errorf("ghtmx is not in the path (%q). You can install ghtmx with `go install github.com/go-monolith/ghtmx/cmd/ghtmx@latest`", os.Getenv("PATH"))
}

func getInfo() (d Info) {
	d.OS.GOOS = runtime.GOOS
	d.OS.GOARCH = runtime.GOARCH

	var wg sync.WaitGroup
	wg.Go(func() {
		d.Go = getGoInfo()
	})
	wg.Go(func() {
		d.Gopls = getGoplsInfo()
	})
	wg.Go(func() {
		d.Ghtmx = getGhtmxInfo()
	})
	wg.Wait()
	return
}

func Run(ctx context.Context, log *slog.Logger, stdout io.Writer, args Arguments) (err error) {
	info := getInfo()
	if args.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}
	log.Info("os", slog.String("goos", info.OS.GOOS), slog.String("goarch", info.OS.GOARCH))
	logInfo(ctx, log, "go", info.Go)
	logInfo(ctx, log, "gopls", info.Gopls)
	logInfo(ctx, log, "ghtmx", info.Ghtmx)
	return nil
}

func logInfo(ctx context.Context, log *slog.Logger, name string, ti ToolInfo) {
	args := []any{
		slog.String("location", ti.Location),
		slog.String("version", ti.Version),
	}
	if ti.Message != "" {
		args = append(args, slog.String("message", ti.Message))
	}
	log.Log(ctx, ti.Level, name, args...)
}
