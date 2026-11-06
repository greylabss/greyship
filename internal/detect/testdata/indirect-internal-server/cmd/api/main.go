package main

import (
	"github.com/acme/billing/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		panic(err)
	}
}
