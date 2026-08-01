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
)

type Arguments struct {
	// JSON selects the machine-readable output format.
	JSON bool
	// Dir is the module root; empty means the current directory.
	Dir string
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

	if args.JSON {
		if err := writeJSON(stdout, table); err != nil {
			return err
		}
	} else if err := writeText(stdout, table); err != nil {
		return err
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
	return nil
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

type jsonRoute struct {
	Verb         string   `json:"verb"`
	Path         string   `json:"path"`
	OriginalPath string   `json:"originalPath"`
	Params       []string `json:"params,omitempty"`
	HandlerPkg   string   `json:"handlerPackage"`
	HandlerName  string   `json:"handlerName"`
	Origin       string   `json:"origin"`
	Recognizer   string   `json:"recognizer"`
	Source       string   `json:"source"`
}

func writeJSON(w io.Writer, table *routes.Table) error {
	all := table.All()
	out := make([]jsonRoute, 0, len(all))
	for _, r := range all {
		verb := string(r.Verb)
		if r.Verb == routes.AnyVerb {
			verb = "*"
		}
		jr := jsonRoute{
			Verb:         verb,
			Path:         r.Path,
			OriginalPath: r.OriginalPath,
			HandlerPkg:   r.Handler.PkgPath,
			HandlerName:  r.Handler.Name,
			Origin:       string(r.Origin),
			Recognizer:   r.Recognizer,
			Source:       r.Pos.String(),
		}
		for _, p := range r.Params {
			name := p.Name
			if p.Wildcard {
				name += "..."
			}
			jr.Params = append(jr.Params, name)
		}
		out = append(out, jr)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
