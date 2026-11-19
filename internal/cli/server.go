package cli

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/GPUInAntarctica/DevicePort/internal/server"
)

// RunServer starts the signaling, relay and admin API server.
func RunServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	listen := fs.String("listen", "[::]:9000", "TCP listen address (IPv6 by default)")
	admin := fs.String("admin", "127.0.0.1:9001", "admin API listen address (empty disables)")
	subnet := fs.String("subnet", "", "force default network subnet, e.g. 10.66.0.0/24")
	requireAuth := fs.Bool("require-auth", false, "new networks require member authorization by default")
	authNetworks := fs.String("auth-networks", "", "comma-separated networks that require authorization")
	noAuthNetworks := fs.String("noauth-networks", "", "comma-separated networks that never require authorization")
	verbose := fs.Bool("v", false, "verbose logging")
	_ = fs.Parse(args)

	logger := log.New(os.Stderr, "server: ", log.LstdFlags)
	if *verbose {
		logger.SetFlags(log.LstdFlags | log.Lshortfile)
	}
	srv := server.New(logger)
	if *subnet != "" {
		srv.SetDefaultSubnet(*subnet)
	}
	srv.SetDefaultRequireAuth(*requireAuth)
	for _, name := range splitList(*authNetworks) {
		srv.SetNetworkAuth(name, true)
	}
	for _, name := range splitList(*noAuthNetworks) {
		srv.SetNetworkAuth(name, false)
	}

	if *admin != "" {
		go func() {
			logger.Printf("admin API listening on %s", *admin)
			if err := http.ListenAndServe(*admin, srv.AdminHandler()); err != nil {
				logger.Printf("admin API stopped: %v", err)
			}
		}()
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

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
