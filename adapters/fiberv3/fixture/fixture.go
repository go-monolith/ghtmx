// Package fixture is the fiber v3 fixture application: a minimal app
// proving the adapter builds and serves against a real fiber engine. The
// adapter tests drive it through fiber's in-memory Test transport, and
// fixture/cmd serves it standalone.
package fixture

import (
	"context"
	"io"
	"net/http"

	fiberfw "github.com/gofiber/fiber/v3"

	"github.com/go-monolith/ghtmx"
	fiberadapter "github.com/go-monolith/ghtmx/adapters/fiberv3"
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

// NewApp wires the adapter into a fiber app.
func NewApp() *fiberfw.App {
	app := fiberfw.New()
	app.Get("/items", func(c fiberfw.Ctx) error {
		return fiberadapter.Render(c, fiberadapter.WithPage(ItemsPage(), ItemRow()))
	})
	app.Post("/items", func(c fiberfw.Ctx) error {
		return fiberadapter.Render(c, fiberadapter.WithPage(ItemsPage(), ItemRow()),
			fiberadapter.Status(http.StatusCreated), fiberadapter.Retarget("#items"))
	})
	return app
}
