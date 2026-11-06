package router

import "github.com/gin-gonic/gin"

func Serve(addr string) {
	r := gin.New()
	r.Run(addr)
}
