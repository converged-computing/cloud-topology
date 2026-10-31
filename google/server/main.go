// Command server serves the static database over HTTP.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/converged-computing/google-cloud-topology/pkg/server"
)

func main() {
	dataDir := flag.String("data", "data", "database directory")
	addr := flag.String("addr", ":8080", "listen address")
	quotaFile := flag.String("quota", "", "optional quota snapshot (from quota fetch) to size graphs by quota")
	flag.Parse()

	srv, err := server.New(*dataDir, *quotaFile)
	if err != nil {
		log.Fatal(err)
	}

	// Reload the database from disk on SIGHUP, after files are updated.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			if err := srv.Reload(); err != nil {
				log.Printf("reload failed, keeping previous database: %v", err)
				continue
			}
			log.Printf("reloaded database from %s", *dataDir)
		}
	}()

	log.Printf("serving %s on %s", *dataDir, *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
