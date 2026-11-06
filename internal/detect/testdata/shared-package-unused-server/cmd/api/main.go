package main

import (
	"github.com/acme/x/internal/platform"
)

func main() {
	platform.Setup()
	platform.ServeAPI()
}
