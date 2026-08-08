// Package fixture is the revel fixture application: a minimal app
// proving the adapter renders through revel's real controller, Result,
// and Go server-engine types. revel has no standalone router — its mux
// only assembles inside a generated app with conf/routes — so the
// fixture dispatches GET and POST /items with net/http's mux and hands
// each request to a revel controller exactly as revel's request loop
// does: bind the engine context, wrap it in a Controller, invoke the
// action, and Apply the returned Result. The adapter tests drive it
// over HTTP, and fixture/cmd serves it standalone.
package fixture

import (
	"context"
	"io"
	"net/http"

	revelfw "github.com/revel/revel"

	"github.com/go-monolith/ghtmx"
	reveladapter "github.com/go-monolith/ghtmx/adapters/revel"
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

// Items is the fixture controller. Its actions return the adapter's
// Result — revel's idiom for producing a response.
type Items struct {
	*revelfw.Controller
}

// Index serves GET /items.
func (c Items) Index() revelfw.Result {
	return reveladapter.Result(c.Controller, reveladapter.WithPage(ItemsPage(), ItemRow()))
}

// Create serves POST /items.
func (c Items) Create() revelfw.Result {
	return reveladapter.Result(c.Controller, reveladapter.WithPage(ItemsPage(), ItemRow()),
		reveladapter.Status(http.StatusCreated), reveladapter.Retarget("#items"))
}

// NewHandler wires the fixture actions into an http.Handler.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", action(Items.Index))
	mux.HandleFunc("POST /items", action(Items.Create))
	return mux
}

// action adapts one controller action to net/http through revel's own
// Go engine types — the ServerContext implementation a served revel app
// binds — applying the returned Result as revel's request loop would.
func action(invoke func(Items) revelfw.Result) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		engine := revelfw.NewGoContext(nil)
		engine.Request.SetRequest(r)
		engine.Response.SetResponse(w)
		c := revelfw.NewController(engine)
		c.Log = revelfw.AppLog.New("path", r.URL.Path, "method", r.Method)
		if result := invoke(Items{Controller: c}); result != nil {
			result.Apply(c.Request, c.Response)
		}
	}
}
