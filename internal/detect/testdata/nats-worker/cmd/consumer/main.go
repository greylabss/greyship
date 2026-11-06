package main

import (
	"github.com/nats-io/nats.go"
	"runtime"
)

func main() {
	nc, _ := nats.Connect(nats.DefaultURL)
	nc.Subscribe("orders", func(m *nats.Msg) {})
	runtime.Goexit()
}
