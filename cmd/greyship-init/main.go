// greyship-init is PID 1 in every instance; see internal/guestinit.
package main

import (
	"fmt"
	"os"

	"greyship.tech/internal/guestinit"
)

const app = "/app"

func main() {
	cfg, err := guestinit.ReadConfig(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "greyship-init:", err)
		os.Exit(1)
	}
	code, err := guestinit.Run(app, cfg, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "greyship-init:", err)
	}
	os.Exit(code)
}
