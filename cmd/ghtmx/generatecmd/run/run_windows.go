//go:build windows

package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

var (
	m       = &sync.Mutex{}
	running = map[string]*exec.Cmd{}
)

// killTree force-kills pid and its children. taskkill exits 128 when
// the process is already gone and 255 when part of the tree vanished
// while it walked it — both mean the tree is already dying, which is
// the outcome we wanted, so they are not errors.
func killTree(pid int) error {
	kill := exec.Command("TASKKILL", "/T", "/F", "/PID", strconv.Itoa(pid))
	kill.Stderr = os.Stderr
	kill.Stdout = os.Stdout
	err := kill.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code == 128 || code == 255 {
			return nil
		}
	}
	return err
}

func KillAll() (err error) {
	m.Lock()
	defer m.Unlock()
	for _, cmd := range running {
		if err := killTree(cmd.Process.Pid); err != nil {
			return err
		}
	}
	running = map[string]*exec.Cmd{}
	return
}

func Stop(cmd *exec.Cmd) (err error) {
	return killTree(cmd.Process.Pid)
}

func Run(ctx context.Context, workingDir string, input string) (cmd *exec.Cmd, err error) {
	m.Lock()
	defer m.Unlock()
	cmd, ok := running[input]
	if ok {
		if err := killTree(cmd.Process.Pid); err != nil {
			return cmd, err
		}
		delete(running, input)
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd = exec.Command(shell, "/C", input)
	cmd.Env = os.Environ()
	cmd.Dir = workingDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return cmd, err
	}
	running[input] = cmd
	return cmd, nil
}
