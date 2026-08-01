package testcssmiddleware

import (
	_ "embed"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/internal/htmldiff"
	"github.com/google/go-cmp/cmp"
)

//go:embed expected.html
var expected string

var expectedCSS = ".red_050e5e03{color:red;}"

func Test(t *testing.T) {
	component := render("Red text")
	h := ghtmx.Handler(component)
	cssmw := ghtmx.NewCSSMiddleware(h, red())

	w := httptest.NewRecorder()
	cssmw.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	actualHTML, diffHTML, err := htmldiff.DiffStrings(expected, w.Body.String())
	if err != nil {
		t.Error(err)
	}
	if diffHTML != "" {
		if err := os.WriteFile("actual.html", []byte(actualHTML), 0644); err != nil {
			t.Errorf("failed to write actual.html: %v", err)
		}
		t.Error(diffHTML)
	}

	wcss := httptest.NewRecorder()
	cssmw.ServeHTTP(wcss, httptest.NewRequest("GET", "/styles/ghtmx.css", nil))
	if diff := cmp.Diff(expectedCSS, wcss.Body.String()); diff != "" {
		t.Error(diff)
	}
}
