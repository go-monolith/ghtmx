package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sync:", err)
		os.Exit(1)
	}
}

func run() error {
	entries, err := Entries()
	if err != nil {
		return err
	}
	for _, e := range entries {
		data, err := os.ReadFile(e.Src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(e.Dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(e.Dst, data, 0o644); err != nil {
			return err
		}
	}
	extra, err := orphans(entries)
	if err != nil {
		return err
	}
	for _, path := range extra {
		if err := os.Remove(path); err != nil {
			return err
		}
		fmt.Println("removed orphan", path)
	}
	fmt.Printf("synced %d files\n", len(entries))
	return nil
}
