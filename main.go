// Command task032-cachepolicy runs the cache eviction policy service.
//
// Use --smoke-test to run the built-in self-check, which exits the process on
// completion. Otherwise it serves the HTTP API with `server --addr :8080`.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"task032-cachepolicy/internal/httpapi"
	"task032-cachepolicy/internal/selfcheck"
)

func main() {
	var smokeTest bool
	var addr string
	usage := func(fs *flag.FlagSet) {
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s --smoke-test                 run self-check and exit\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s server --addr :8080          start the HTTP server\n", os.Args[0])
		fs.PrintDefaults()
	}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&smokeTest, "smoke-test", false, "run the self-check and exit")
	fs.StringVar(&addr, "addr", ":8080", "HTTP listen address")
	fs.Usage = func() { usage(fs) }

	args := os.Args[1:]
	if len(args) > 0 && args[0] == "server" {
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if smokeTest {
		if err := selfcheck.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "smoke-test FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("smoke-test PASSED")
		return
	}

	// Support bare invocation with flags as well as the explicit server command.
	if rest := fs.Args(); len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "unknown argument: %s\n", rest[0])
		os.Exit(2)
	}

	srv := httpapi.New()
	hs := &http.Server{
		Addr:    addr,
		Handler: srv.Handler(),
	}
	log.Printf("task032-cachepolicy listening on %s", addr)
	if err := hs.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
