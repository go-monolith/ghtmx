module github.com/go-monolith/ghtmx/docs/official

go 1.25.0

require (
	github.com/go-chi/chi/v5 v5.3.1
	github.com/go-monolith/ghtmx v0.1.1
	github.com/go-monolith/ghtmx/adapters/chi v0.1.1
	github.com/syumai/workers v0.33.0
	github.com/yuin/goldmark v1.8.5
)

replace github.com/go-monolith/ghtmx => ../..

replace github.com/go-monolith/ghtmx/adapters/chi => ../../adapters/chi
