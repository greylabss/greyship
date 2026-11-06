package main

import (
	"google.golang.org/grpc"
	"net"
)

func main() {
	lis, _ := net.Listen("tcp", ":50051")
	s := grpc.NewServer()
	s.Serve(lis)
}
