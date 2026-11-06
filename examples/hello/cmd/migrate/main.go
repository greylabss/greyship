// The release command: Greyship runs it once per deploy, before the new version takes traffic.
// A real app would run its database migrations here; exiting non-zero stops the deploy.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("running migrations… (DATABASE_URL set:", os.Getenv("DATABASE_URL") != "", ")")
	fmt.Println("migrations up to date")
}
