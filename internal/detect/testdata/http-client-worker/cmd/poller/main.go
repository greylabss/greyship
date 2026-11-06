package main

import (
	"net/http"
	"time"
)

func main() {
	for {
		resp, err := http.Get("https://example.com/feed")
		if err == nil {
			resp.Body.Close()
		}
		time.Sleep(time.Minute)
	}
}
