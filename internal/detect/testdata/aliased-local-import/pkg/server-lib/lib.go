package serverlib

import stdhttp "net/http"

func Listen() {
	stdhttp.ListenAndServe(":8080", nil)
}
