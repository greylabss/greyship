package main

import (
	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	_ = connect.NewError
	http.ListenAndServe("localhost:8080", h2c.NewHandler(mux, &http2.Server{}))
}
