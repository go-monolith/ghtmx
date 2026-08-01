package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/go-monolith/ghtmx"
)

var flagPort = flag.Int("port", 0, "Set the HTTP listen port")

var count int

func home(w http.ResponseWriter, r *http.Request) {
	count++
	c := Page(count)
	ghtmx.Handler(c).ServeHTTP(w, r)
}

func main() {
	flag.Parse()

	if *flagPort == 0 {
		fmt.Println("missing port flag")
		os.Exit(1)
	}

	http.HandleFunc("/", home)
	err := http.ListenAndServe(fmt.Sprintf("localhost:%d", *flagPort), nil)
	if err != nil {
		fmt.Printf("Error listening: %v\n", err)
		os.Exit(1)
	}
}
