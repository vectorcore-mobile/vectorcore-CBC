package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vectorcore/cbc/internal/cbs"
	"github.com/vectorcore/cbc/internal/config"
	"github.com/vectorcore/cbc/internal/delivery"
	"github.com/vectorcore/cbc/internal/geocode"
	"github.com/vectorcore/cbc/internal/httpapi"
	"github.com/vectorcore/cbc/internal/inventory"
	"github.com/vectorcore/cbc/internal/service"
	"github.com/vectorcore/cbc/internal/storage/sqlite"
	"github.com/vectorcore/cbc/internal/xmpp"
)

var version = "dev"

func main() {
	path := flag.String("c", "config/cbc.yaml", "configuration file")
	debug := flag.Bool("d", false, "debug logging")
	showVersion := flag.Bool("v", false, "print version")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	flag.Parse()
	if *showVersion {
		_, _ = os.Stdout.WriteString(version + "\n")
		return
	}
	cfg, err := config.Load(*path)
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	if *checkConfig {
		_, _ = os.Stdout.WriteString("configuration valid\n")
		return
	}
	_, _ = os.Stdout.WriteString("Starting VectorCore CBC\n")
	logFile, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Error("log file open failed", "error", err)
		os.Exit(1)
	}
	defer logFile.Close()
	fileLevel := new(slog.LevelVar)
	fileLevel.Set(parseLevel(cfg.Logging.Level))
	var handler slog.Handler = slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: fileLevel})
	if *debug {
		handler = newMultiHandler(handler, slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	slog.SetDefault(slog.New(handler))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	store, err := sqlite.Open(ctx, cfg.Database.Path, cfg.Database.BusyTimeout)
	if err != nil {
		slog.Error("database open failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		slog.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	var invService *inventory.Service
	var geoService *geocode.Service
	if cfg.CellInventory.Enabled {
		invService = inventory.NewService(store, inventory.NewGoSpatialMatcher(), cfg.CellInventory.MaxImportSizeBytes)
		geoService = geocode.NewService(store)
	}
	preparer := cbs.New(cfg.CBS, store)
	if invService != nil {
		// Resolves CAP <polygon> areas to real LTE cells via the cell
		// inventory - without this, only alerts carrying an explicit
		// cell/TAC geocode (not what real CAP alerts, e.g. NWS WEA, send)
		// can be targeted.
		preparer.SetCellSelector(cfg.SBcAP.PLMN, invService)
		// Resolves CAP <geocode> SAME/UGC entries to real LTE cells via the
		// operator-curated code->cell mapping.
		preparer.SetGeocodeResolver(geoService)
	}
	publisher := delivery.New(cfg.SBcAP, preparer, store)
	defer publisher.Close()
	svc := service.New(store, publisher)
	publisher.SetMetricsRecorder(svc)
	if err = svc.Recover(ctx); err != nil {
		slog.Error("alert recovery failed", "error", err)
		os.Exit(1)
	}
	go expireLoop(ctx, svc, cfg.Database.ExpiryInterval)
	// Per TS 29.168 the CBC establishes and maintains the MME SCTP
	// association at all times, independent of alert traffic - started here
	// rather than lazily on first Publish, so an eNB restart indication is
	// never missed for lack of a connection.
	go publisher.Run(ctx)
	client := xmpp.New(cfg.CBE, svc.Ingest, func(connected bool, err error) {
		svc.SetConnected(connected)
		svc.SetError(err)
		if err != nil && ctx.Err() == nil {
			slog.Warn("CBE XMPP session ended", "error", err)
		}
		if connected {
			slog.Info("CBE XMPP session established", "address", cfg.CBE.Address)
		}
	})
	go client.Run(ctx)
	server := &http.Server{Addr: cfg.Server.ListenAddress, Handler: httpapi.New(svc, invService, geoService, cfg.CellInventory.DefaultImportMode, version), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("cbc operator API listening", "address", cfg.Server.ListenAddress)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("operator API failed", "error", err)
		}
	}()
	<-ctx.Done()
	stop, done := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer done()
	_ = server.Shutdown(stop)
}

func expireLoop(ctx context.Context, svc *service.Service, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := svc.Expire(context.Background(), now.UTC()); err != nil {
				slog.Warn("alert expiry failed", "error", err)
			}
		}
	}
}
