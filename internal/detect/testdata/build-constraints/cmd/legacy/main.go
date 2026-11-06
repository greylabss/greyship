//go:build darwin
// +build darwin

package main

import (
	"log"
	"time"
)

func main() {
	for {
		log.Println("tick")
		time.Sleep(time.Minute)
	}
}
