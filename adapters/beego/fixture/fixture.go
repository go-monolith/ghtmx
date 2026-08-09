// Package fixture is the beego fixture application: a minimal app
// proving the adapter builds and serves against a real beego router.
// The adapter tests drive it over HTTP, and fixture/cmd serves it
// standalone.
package fixture

import (
	"context"
	"io"
	"log"
	"net/http"

	"github.com/beego/beego/v2/server/web"
	beegocontext "github.com/beego/beego/v2/server/web/context"

	"github.com/go-monolith/ghtmx"
	beegoadapter "github.com/go-monolith/ghtmx/adapters/beego"
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

// NewRouter wires the adapter into a beego controller register — an
// http.Handler served directly, without the global web.Run path or an
// app.conf.
func NewRouter() *web.ControllerRegister {
	router := web.NewControllerRegister()
	router.Get("/items", func(ctx *beegocontext.Context) {
		if err := beegoadapter.Render(ctx, beegoadapter.WithPage(ItemsPage(), ItemRow())); err != nil {
			log.Printf("render /items: %v", err)
		}
	})
	router.Post("/items", func(ctx *beegocontext.Context) {
		err := beegoadapter.Render(ctx, beegoadapter.WithPage(ItemsPage(), ItemRow()),
			beegoadapter.Status(http.StatusCreated), beegoadapter.Retarget("#items"))
		if err != nil {
			log.Printf("render /items: %v", err)
		}
	})
	return router
}
