package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
	"github.com/msnow/vunet-dante-combiner-2000/internal/inventory"
	"github.com/msnow/vunet-dante-combiner-2000/internal/reflector"
	"github.com/msnow/vunet-dante-combiner-2000/internal/statushttp"
)

func main() {
	cfgPath := flag.String("config", "/etc/combiner/site.yaml", "path to site.yaml")
	checkOnly := flag.Bool("check", false, "validate config and exit")
	flag.Parse()

	site, err := config.LoadSite(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *checkOnly {
		log.Printf("config OK (%d allowlists, %d deny prefixes)", len(site.Allowlists), len(site.DenyPrefixes))
		os.Exit(0)
	}

	inv := inventory.New()
	ref, err := reflector.New(site, inv)
	if err != nil {
		log.Fatalf("reflector: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		// Reflector errors are degraded (logged); status page stays up.
		if err := ref.Run(ctx); err != nil {
			log.Printf("reflector stopped: %v", err)
		}
	}()

	srv := &statushttp.Server{Site: site, Inv: inv, Ref: ref}
	httpSrv := &http.Server{
		Addr:              site.StatusListen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	go func() {
		log.Printf("status listening on %s", site.StatusListen)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http: %v", err)
			cancel()
		}
	}()

	<-ctx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
}
