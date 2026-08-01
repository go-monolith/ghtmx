// Package fixture is the echo fixture application: a minimal app
// proving the adapter builds and serves against a real echo engine. The
// adapter tests drive it over HTTP, and fixture/cmd serves it
// standalone.
package fixture

import (
	"context"
	"io"
	"net/http"

	echofw "github.com/labstack/echo/v4"

	"github.com/go-monolith/ghtmx"
	echoadapter "github.com/go-monolith/ghtmx/adapters/echo"
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

// NewRouter wires the adapter into an echo engine.
func NewRouter() *echofw.Echo {
	e := echofw.New()
	e.HideBanner = true
	e.GET("/items", func(c echofw.Context) error {
		return echoadapter.Render(c, echoadapter.WithPage(ItemsPage(), ItemRow()))
	})
	e.POST("/items", func(c echofw.Context) error {
		return echoadapter.Render(c, echoadapter.WithPage(ItemsPage(), ItemRow()),
			echoadapter.Status(http.StatusCreated), echoadapter.Retarget("#items"))
	})
	return e
}
