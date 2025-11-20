package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"k8s.io/klog"
)

type Server struct {
	http             http.Server
	shutdownFinished chan struct{}
}

func (server *Server) ListenAndServe() {
	err := server.http.ListenAndServe()

	if errors.Is(err, http.ErrServerClosed) {
		// Expected in case of shutdown.
		err = nil
	} else {
		klog.Warning("HTTP server failed: " + err.Error())
	}

	<-server.shutdownFinished
}

func (server *Server) shutdown(waitForExit context.Context, exitTimeout time.Duration) {
	<-waitForExit.Done()

	ctx, cancel := context.WithTimeout(context.Background(), exitTimeout)
	defer cancel()

	err := server.http.Shutdown(ctx)
	if err != nil {
		klog.Warning("Error while shutting down HTTP server: " + err.Error())
	} else {
		klog.Info("HTTP server successfully shut down.")
	}
	close(server.shutdownFinished)
}

type Observable func()

func poll(ctx context.Context, period time.Duration, observe Observable) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			observe()
		}
	}
}

type ServerConfig struct {
	Host            string
	Port            int16
	PathPrefix      string
	PollPeriod      time.Duration
	ShutdownTimeout time.Duration
	Enabled         bool
}

func (config *ServerConfig) CommandLineParameters(root *cobra.Command) {
	root.PersistentFlags().StringVar(&config.Host, "metrics-host", config.Host, "Host name or ip address for Prometheus metrics")
	root.PersistentFlags().Int16Var(&config.Port, "metrics-port", config.Port, "Port for Prometheus metrics")
	root.PersistentFlags().StringVar(&config.PathPrefix, "metrics-path-prefix", config.PathPrefix, "Path prefix for Prometheus metrics")
	root.PersistentFlags().DurationVar(&config.PollPeriod, "metrics-poll-period", config.PollPeriod, "Polling period for Prometheus metrics updates")
	root.PersistentFlags().DurationVar(&config.ShutdownTimeout, "metrics-shutdown-timeout", config.ShutdownTimeout, "Shutdown timeout of the Prometheus metrics server")
	root.PersistentFlags().BoolVar(&config.Enabled, "metrics-enabled", config.Enabled, "Prometheus metrics state")
}

func (config *ServerConfig) NewServer(ctx context.Context, meters *[]Observable) *Server {
	mux := http.NewServeMux()
	mux.Handle(config.PathPrefix, promhttp.Handler())

	server := Server{
		http:             http.Server{Addr: fmt.Sprintf("%s:%d", config.Host, config.Port), Handler: mux},
		shutdownFinished: make(chan struct{}),
	}

	go poll(ctx, config.PollPeriod,
		func() {
			for _, observer := range *meters {
				observer()
			}
		},
	)

	go server.shutdown(ctx, config.ShutdownTimeout)

	return &server
}
