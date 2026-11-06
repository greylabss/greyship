// A test app for the agent's integration tests. MODE picks what it does.
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	fmt.Printf("env GOMAXPROCS=%s GOMEMLIMIT=%s GREYSHIP_SERVICE=%s PORT=%s\n", os.Getenv("GOMAXPROCS"), os.Getenv("GOMEMLIMIT"), os.Getenv("GREYSHIP_SERVICE"), os.Getenv("PORT"))
	fmt.Printf("uid %d\n", os.Getuid())
	switch os.Getenv("MODE") {
	case "exit":
		fmt.Fprintln(os.Stderr, "boom: missing DATABASE_URL")
		os.Exit(2)
	case "silent", "worker":
		time.Sleep(time.Hour)
	}
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "hello from greyship") })
	// /file?name=… reads a repo file by its relative path, as apps using greyship.toml's files do.
	http.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(r.URL.Query().Get("name"))
		if err != nil {
			fmt.Fprint(w, "error: ", err)
			return
		}
		_, _ = w.Write(b)
	})
	// /write tries to change a shipped file, which must fail: the files are read-only.
	http.HandleFunc("/write", func(w http.ResponseWriter, _ *http.Request) {
		if err := os.WriteFile("scripts/install.sh", []byte("changed"), 0o644); err != nil {
			fmt.Fprint(w, "refused")
			return
		}
		fmt.Fprint(w, "written")
	})
	// /probe?addr=host:port reports whether this instance can open a TCP connection there.
	http.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		c, err := net.DialTimeout("tcp", r.URL.Query().Get("addr"), 3*time.Second)
		if err != nil {
			fmt.Fprint(w, "blocked")
			return
		}
		_ = c.Close()
		fmt.Fprint(w, "open")
	})
	// ADDR hard-codes where the app listens, ignoring $PORT; EXTRA opens a second port, like pprof would.
	addr := ":" + os.Getenv("PORT")
	if a := os.Getenv("ADDR"); a != "" {
		addr = a
	}
	if extra := os.Getenv("EXTRA"); extra != "" {
		go func() { _ = http.ListenAndServe(extra, http.NotFoundHandler()) }()
	}
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
