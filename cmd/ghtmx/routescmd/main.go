// Package routescmd implements `ghtmx routes`: it prints the discovered
// route table for debugging why a binding did or did not resolve (FR-064).
package routescmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"text/tabwriter"

	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/routes"
	"github.com/go-monolith/ghtmx/routetable"
)

type Arguments struct {
	// JSON selects the machine-readable output format.
	JSON bool
	// Dir is the module root; empty means the current directory.
	Dir string
	// CheckAgainst is a JSON file holding the routes the application's
	// own router serves; when set, the command compares the discovered
	// table against it instead of printing, and exits non-zero on any
	// mismatch.
	CheckAgainst string
}

func Run(log *slog.Logger, stdout io.Writer, args Arguments) error {
	dir := args.Dir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	sink := diag.NewSink(cfg.SeverityOverrides())
	pkgs, err := routes.Load(dir, cfg.RouteScope, sink)
	if err != nil {
		return err
	}
	table := routes.Discover(pkgs, sink)

	mismatched := false
	switch {
	case args.CheckAgainst != "":
		mismatched, err = checkAgainst(stdout, table, args.CheckAgainst)
		if err != nil {
			return err
		}
	case args.JSON:
		if err := writeJSON(stdout, table); err != nil {
			return err
		}
	default:
		if err := writeText(stdout, table); err != nil {
			return err
		}
	}

	for _, d := range sink.Diagnostics() {
		attrs := []any{slog.String("id", d.ID), slog.String("pos", d.Pos.String())}
		if d.Suggest != "" {
			attrs = append(attrs, slog.String("suggest", d.Suggest))
		}
		if d.Severity == diag.Error {
			log.Error(d.Message, attrs...)
			continue
		}
		log.Warn(d.Message, attrs...)
	}
	if sink.HasErrors() {
		return fmt.Errorf("route discovery reported errors")
	}
	if mismatched {
		return fmt.Errorf("the discovered route table does not match %s", args.CheckAgainst)
	}
	return nil
}

// checkAgainst compares the discovered table against a JSON list of the
// routes the application's router actually serves, printing every
// mismatch. It reports whether any were found (FR-064): the annotation
// escape hatch moves registration outside routeScope, and this is what
// keeps the two from drifting apart unnoticed.
func checkAgainst(w io.Writer, table *routes.Table, path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("failed to read %s: %w", path, err)
	}
	var actual []routetable.Route
	if err := json.Unmarshal(data, &actual); err != nil {
		return false, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	mismatches := routetable.Diff(routetable.FromTable(table), actual)
	if len(mismatches) == 0 {
		return false, nil
	}
	if _, err := io.WriteString(w, routetable.Report(mismatches)); err != nil {
		return false, err
	}
	return true, nil
}

func writeText(w io.Writer, table *routes.Table) error {
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "VERB\tPATH\tHANDLER\tORIGIN\tRECOGNIZER\tSOURCE"); err != nil {
		return err
	}
	for _, r := range table.All() {
		verb := string(r.Verb)
		if r.Verb == routes.AnyVerb {
			verb = "*"
		}
		origin := string(r.Origin)
		if r.Origin == routes.Declared {
			// Escape-hatch declarations are marked distinctly (FR-064).
			origin = "declared (//ghtmx:route)"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", verb, r.Path, r.Handler, origin, r.Recognizer, r.Pos); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// writeJSON emits the public routetable.Route shape, so the command's
// output and the package a consumer unmarshals it into cannot describe
// the same route differently.
func writeJSON(w io.Writer, table *routes.Table) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(routetable.FromTable(table))
}
