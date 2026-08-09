// Package fixture is the martini fixture application: a minimal app
// proving the adapter builds and serves against a real martini
// instance. The adapter tests drive it through httptest, and
// fixture/cmd serves it standalone.
package fixture

import (
	"context"
	"io"
	"log"
	"net/http"

	martinifw "github.com/go-martini/martini"
	"github.com/go-monolith/ghtmx"
	martiniadapter "github.com/go-monolith/ghtmx/adapters/martini"
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

// NewApp wires the adapter into a bare martini instance: martini.New
// with a router mapped in, not martini.Classic, whose logger middleware
// would write every request to stdout. The instance is an http.Handler.
func NewApp() http.Handler {
	m := martinifw.New()
	r := martinifw.NewRouter()
	m.MapTo(r, (*martinifw.Routes)(nil))
	m.Action(r.Handle)
	r.Get("/items", func(w http.ResponseWriter, req *http.Request) {
		if err := martiniadapter.Render(w, req, martiniadapter.WithPage(ItemsPage(), ItemRow())); err != nil {
			// The response may be partially written by now, so no error
			// page can be sent reliably: log and let the client see the
			// truncated response (see the nethttp adapter docs).
			log.Printf("render /items: %v", err)
		}
	})
	r.Post("/items", func(w http.ResponseWriter, req *http.Request) {
		err := martiniadapter.Render(w, req, martiniadapter.WithPage(ItemsPage(), ItemRow()),
			martiniadapter.Status(http.StatusCreated), martiniadapter.Retarget("#items"))
		if err != nil {
			log.Printf("render /items: %v", err)
		}
	})
	return m
}
