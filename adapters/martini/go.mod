module github.com/go-monolith/ghtmx/adapters/martini

go 1.25.0

require (
	github.com/go-martini/martini v0.0.0-20170121215854-22fa46961aab
	github.com/go-monolith/ghtmx v0.1.18
)

require github.com/codegangsta/inject v0.0.0-20150114235600-33e0aa1cb7c0 // indirect

replace github.com/go-monolith/ghtmx => ../..
