module github.com/go-monolith/ghtmx/internal/wasmcheck/fixture

go 1.25.0

require (
	github.com/go-chi/chi/v5 v5.3.1
	github.com/go-monolith/ghtmx v0.1.12
	github.com/go-monolith/ghtmx/adapters/chi v0.1.12
)

replace github.com/go-monolith/ghtmx => ../../..

replace github.com/go-monolith/ghtmx/adapters/chi => ../../../adapters/chi
