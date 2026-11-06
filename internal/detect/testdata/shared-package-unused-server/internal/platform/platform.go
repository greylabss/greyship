package platform

import "net/http"

func Setup() {}

func ServeAPI() {
	http.ListenAndServe(":8080", nil)
}
