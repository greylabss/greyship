package app

import "github.com/acme/billing/internal/httpserver"

func Run() error {
	return httpserver.New().Start()
}
