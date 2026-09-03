// The htmx4-status example: htmx 4 swaps every response by default, and
// hx-status:<code> decides per status where it goes. A signup form
// routes its 422 validation errors to #errors, ignores 5xx bodies, and
// on success updates a second region through <hx-partial>, the htmx 4
// form of an out-of-band swap. The compiler validates the status
// suffixes, the config keys, and the partial's target against the
// 4.0.0 pin in ghtmx.json.
//
// Run it with:
//
//	ghtmx generate && go run ./cmd
package htmx4status

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/go-monolith/ghtmx"
)

//go:embed status.css
var styleCSS string

// styleSheet inlines status.css into the page head. The rules live in
// their own file so the template shows markup, not presentation.
func styleSheet() ghtmx.Component {
	return ghtmx.Raw("<style>" + styleCSS + "</style>")
}

func signupHome(w http.ResponseWriter, r *http.Request) {
	if err := page().Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

// validate returns the problems with a signup, in display order.
func validate(email, handle string) []string {
	var problems []string
	if !strings.Contains(email, "@") {
		problems = append(problems, "email needs an @")
	}
	switch {
	case handle == "":
		problems = append(problems, "handle is required")
	case strings.ContainsAny(handle, " \t"):
		problems = append(problems, "handle cannot contain spaces")
	}
	return problems
}

// Signup answers with the status the outcome deserves and lets the
// markup route each one: 422 carries the error list (hx-status:422
// sends it to #errors), 500 carries a body nobody should see
// (hx-status:5xx swaps nothing), 200 carries the welcome plus a partial
// for the "last signup" region.
func Signup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("outage") != "" {
		http.Error(w, "<p>simulated outage — this body must not be swapped in</p>", http.StatusInternalServerError)
		return
	}
	email := strings.TrimSpace(r.PostForm.Get("email"))
	handle := strings.TrimSpace(r.PostForm.Get("handle"))
	if problems := validate(email, handle); len(problems) > 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		if err := validationErrorsFragment(problems).RenderFragment(r.Context(), w); err != nil {
			http.Error(w, "failed to render", http.StatusInternalServerError)
		}
		return
	}
	if err := welcomeFragment(handle, email).RenderFragment(r.Context(), w); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}

// Routes builds the example's router; the official docs site mounts
// it as a live demo.
func Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /signup", signupHome)
	mux.HandleFunc("POST /signup", Signup)
	return mux
}
