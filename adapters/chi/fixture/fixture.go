// Package fixture is the chi fixture application: a minimal app proving
// the adapter builds and serves against a real chi router. The adapter
// tests drive it over HTTP, and fixture/cmd serves it standalone.
package fixture

import (
	"context"
	"io"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-monolith/ghtmx"
	chiadapter "github.com/go-monolith/ghtmx/adapters/chi"
)

// ItemsPage is the full page a browser request receives.
func ItemsPage() ghtmx.Component {
	return ghtmx.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<html><body><ul id="items"><li>alpha</li></ul></body></html>`)
		return err
	})
}

// ItemRow is the standalone fragment an htmx request receives.
func ItemRow() ghtmx.Fragment { return itemRow{} }

type itemRow struct{}

func (itemRow) Render(ctx context.Context, w io.Writer) error {
	_, err := io.WriteString(w, `<li>alpha</li>`)
	return err
}

func (itemRow) RenderFragment(ctx context.Context, w io.Writer) error {
	return itemRow{}.Render(ctx, w)
}

// NewRouter wires the adapter into a chi router.
func NewRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/items", func(w http.ResponseWriter, req *http.Request) {
		if err := chiadapter.Render(w, req, chiadapter.WithPage(ItemsPage(), ItemRow())); err != nil {
			// The response may be partially written by now, so no error
			// page can be sent reliably: log and let the client see the
			// truncated response (see the nethttp adapter docs).
			log.Printf("render /items: %v", err)
		}
	})
	r.Post("/items", func(w http.ResponseWriter, req *http.Request) {
		err := chiadapter.Render(w, req, chiadapter.WithPage(ItemsPage(), ItemRow()),
			chiadapter.Status(http.StatusCreated), chiadapter.Retarget("#items"))
		if err != nil {
			log.Printf("render /items: %v", err)
		}
	})
	return r
}
