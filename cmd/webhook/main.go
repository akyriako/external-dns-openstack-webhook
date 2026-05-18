package main

import (
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/pflag"

	"external-dns-opentelekomcloud-webhook/internal/designate/provider"
	"external-dns-opentelekomcloud-webhook/internal/metrics"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/provider/webhook/api"
)

const (
	webhookServerAddr = "127.0.0.1:8888"
	statusServerAddr  = "0.0.0.0:8080"
)

func main() {
	var domainFilters []string
	var debugLevel int

	pflag.StringArrayVar(&domainFilters, "domain-filter", []string{}, "List of domains to work on (can be specified multiple times)")
	pflag.IntVar(&debugLevel, "debug-level", 0, "Log Level")
	pflag.Parse()

	logger := slog.New(
		slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.Level(debugLevel),
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				switch a.Key {
				case slog.TimeKey:
					t := a.Value.Time()
					return slog.String("time", t.Format("2006-01-02T15:04:05.00Z"))
				case slog.LevelKey:
					return slog.String("level", strings.ToLower(a.Value.String()))
				}

				return a
			},
		}),
	)

	slog.SetDefault(logger)

	startedChan := make(chan struct{})
	httpApiStarted := false

	go func() {
		<-startedChan
		httpApiStarted = true
	}()

	m := http.NewServeMux()
	m.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !httpApiStarted {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	m.HandleFunc("/metrics", promhttp.Handler().ServeHTTP)

	go func() {
		slog.Info("starting status server", "addr", statusServerAddr)
		s := &http.Server{
			Addr:    statusServerAddr,
			Handler: m,
		}

		l, err := net.Listen("tcp", statusServerAddr)
		if err != nil {
			slog.Error("starting status listener failed: %s", err)
			os.Exit(-1)
		}
		err = s.Serve(l)
		if err != nil {
			slog.Error("status listener stopped: %s", err)
			os.Exit(-1)
		}
	}()

	epf := endpoint.NewDomainFilter(domainFilters)
	dp, err := provider.NewDesignateProvider(*epf, false)
	if err != nil {
		slog.Error("creating new DNS provider failed: %v", err)
		metrics.OpenTelekomCloudConnectionMetric.Set(0)

		os.Exit(-1)
	}
	metrics.OpenTelekomCloudConnectionMetric.Set(1)
	slog.Debug("connected to T-Cloud Public API")

	slog.Info("starting webhook server", "addr", webhookServerAddr)
	api.StartHTTPApi(dp, startedChan, 0, 0, webhookServerAddr)
}
