package main

import (
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/spf13/pflag"

	"external-dns-openstack-webhook/internal/designate/provider"
	"external-dns-openstack-webhook/internal/metrics"

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
	pflag.IntVar(&debugLevel, "debug-level", -4, "Log Level")
	pflag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.Level(debugLevel),
	}))

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
		metrics.OpenstackConnectionMetric.Set(0)

		os.Exit(-1)
	}
	metrics.OpenstackConnectionMetric.Set(1)
	slog.Debug("connected to T-Cloud Public API")

	slog.Debug("starting webhook server", "addr", webhookServerAddr)
	api.StartHTTPApi(dp, startedChan, 0, 0, webhookServerAddr)
}
