package main

import (
	"github.com/hibiken/asynq"
)

func main() {
	srv := asynq.NewServer(asynq.RedisClientOpt{Addr: "localhost:6379"}, asynq.Config{})
	mux := asynq.NewServeMux()
	srv.Run(mux)
}
