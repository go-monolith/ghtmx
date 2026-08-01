package main

import "net/http"

// routes registers the named handlers the LSP protocol tests bind to.
func routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", ListItems)
	mux.HandleFunc("POST /items", CreateItem)
	return mux
}

func ListItems(w http.ResponseWriter, r *http.Request) {}

func CreateItem(w http.ResponseWriter, r *http.Request) {}
