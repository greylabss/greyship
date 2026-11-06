package main

import (
	"github.com/gorilla/mux"
	"net/http"
	"time"
)

func main() {
	r := mux.NewRouter()
	srv := &http.Server{Handler: r, Addr: ":8080", ReadTimeout: 5 * time.Second}
	panic(srv.ListenAndServe())
}
