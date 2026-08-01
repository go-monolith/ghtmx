// Package handlers holds the hx-bindings example's HTTP handlers.
package handlers

import "net/http"

func ListItems(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
func GetItem(w http.ResponseWriter, r *http.Request)   { w.WriteHeader(http.StatusOK) }
