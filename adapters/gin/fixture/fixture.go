// Package fixture is the gin fixture application: a minimal app proving
// the adapter builds and serves against a real gin engine. The adapter
// tests drive it over HTTP, and fixture/cmd serves it standalone.
package fixture

import (
	"context"
	"io"
	"log"
	"net/http"

	ginfw "github.com/gin-gonic/gin"

	"github.com/go-monolith/ghtmx"
	ginadapter "github.com/go-monolith/ghtmx/adapters/gin"
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

// NewRouter wires the adapter into a gin engine. Callers pick the gin
// mode themselves: gin.SetMode is process-global state a constructor
// should not mutate.
func NewRouter() *ginfw.Engine {
	r := ginfw.New()
	r.GET("/items", func(c *ginfw.Context) {
		if err := ginadapter.Render(c, ginadapter.WithPage(ItemsPage(), ItemRow())); err != nil {
			// c.Errors is only surfaced by middleware, and this fixture
			// installs none — log so the failure is visible.
			_ = c.Error(err)
			log.Printf("render /items: %v", err)
		}
	})
	r.POST("/items", func(c *ginfw.Context) {
		err := ginadapter.Render(c, ginadapter.WithPage(ItemsPage(), ItemRow()),
			ginadapter.Status(http.StatusCreated), ginadapter.Retarget("#items"))
		if err != nil {
			_ = c.Error(err)
			log.Printf("render /items: %v", err)
		}
	})
	return r
}
