// Command deviceport-server is the DevicePort signaling and relay server.
//
// It assigns virtual IPv4 addresses, introduces peers for IPv6 hole punching,
// and relays encrypted frames when a direct path is unavailable. The server
// never holds the keys and cannot read payloads.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/GPUInAntarctica/DevicePort/internal/server"
)

func main() {
	listen := flag.String("listen", "[::]:9000", "TCP listen address (IPv6 by default)")
	subnet := flag.String("subnet", "", "force default network subnet, e.g. 10.66.0.0/24")
	verbose := flag.Bool("v", false, "verbose logging")
	flag.Parse()

	logger := log.New(os.Stderr, "server: ", log.LstdFlags)
	if *verbose {
		logger.SetFlags(log.LstdFlags | log.Lshortfile)
	}
	srv := server.New(logger)
	if *subnet != "" {
		srv.SetDefaultSubnet(*subnet)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-done
		logger.Println("shutting down")
		os.Exit(0)
	}()

	if err := srv.Serve(*listen); err != nil {
		logger.Fatalf("server stopped: %v", err)
	}
}
