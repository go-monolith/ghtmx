package teststyleattribute

import (
	_ "embed"
	"fmt"
	"os"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/internal/htmldiff"
)

//go:embed expected.html
var expected string

func Test(t *testing.T) {
	var stringCSS = "background-color:blue;color:red"
	var safeCSS = ghtmx.SafeCSS("background-color:blue;color:red;")
	var mapStringString = map[string]string{
		"color":            "red",
		"background-color": "blue",
	}
	var mapStringSafeCSSProperty = map[string]ghtmx.SafeCSSProperty{
		"color":            ghtmx.SafeCSSProperty("red"),
		"background-color": ghtmx.SafeCSSProperty("blue"),
	}
	var kvStringStringSlice = []ghtmx.KeyValue[string, string]{
		ghtmx.KV("background-color", "blue"),
		ghtmx.KV("color", "red"),
	}
	var kvStringBoolSlice = []ghtmx.KeyValue[string, bool]{
		ghtmx.KV("background-color:blue", true),
		ghtmx.KV("color:red", true),
		ghtmx.KV("color:blue", false),
	}
	var kvSafeCSSBoolSlice = []ghtmx.KeyValue[ghtmx.SafeCSS, bool]{
		ghtmx.KV(ghtmx.SafeCSS("background-color:blue"), true),
		ghtmx.KV(ghtmx.SafeCSS("color:red"), true),
		ghtmx.KV(ghtmx.SafeCSS("color:blue"), false),
	}

	tests := []any{
		stringCSS,
		safeCSS,
		mapStringString,
		mapStringSafeCSSProperty,
		kvStringStringSlice,
		kvStringBoolSlice,
		kvSafeCSSBoolSlice,
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%T", test), func(t *testing.T) {
			component := Button(test, "Click me")

			actual, diff, err := htmldiff.Diff(component, expected)
			if err != nil {
				t.Fatal(err)
			}
			if diff != "" {
				if err := os.WriteFile("actual.html", []byte(actual), 0644); err != nil {
					t.Errorf("failed to write actual.html: %v", err)
				}
				t.Error(diff)
			}
		})
	}
}
