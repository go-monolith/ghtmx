// Package fixture is the iris fixture application: a minimal app proving
// the adapter builds and serves against a real iris engine. The adapter
// tests build it and drive it as a plain http.Handler, and fixture/cmd
// serves it standalone.
package fixture

import (
	"context"
	"io"
	"log"
	"net/http"

	irisfw "github.com/kataras/iris/v12"

	"github.com/go-monolith/ghtmx"
	irisadapter "github.com/go-monolith/ghtmx/adapters/iris"
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

// NewApp wires the adapter into an iris application. Callers finish the
// startup themselves: fixture/cmd calls Listen, and the tests call Build
// and then drive the application as an http.Handler.
func NewApp() *irisfw.Application {
	app := irisfw.New()
	app.Get("/items", func(ctx irisfw.Context) {
		if err := irisadapter.Render(ctx, irisadapter.WithPage(ItemsPage(), ItemRow())); err != nil {
			// iris handlers return nothing, and this fixture installs no
			// error middleware — log so the failure is visible.
			log.Printf("render /items: %v", err)
		}
	})
	app.Post("/items", func(ctx irisfw.Context) {
		err := irisadapter.Render(ctx, irisadapter.WithPage(ItemsPage(), ItemRow()),
			irisadapter.Status(http.StatusCreated), irisadapter.Retarget("#items"))
		if err != nil {
			log.Printf("render /items: %v", err)
		}
	})
	return app
}
