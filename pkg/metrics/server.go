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
	host       string
	port       int16
	pathPrefix string
	Enable     bool
}

func (config *ServerConfig) CommandLineParameters(root *cobra.Command) {
	root.PersistentFlags().StringVar(&config.host, "metrics-host", "localhost", "Host name or ip address for Prometheus metrics")
	root.PersistentFlags().Int16Var(&config.port, "metrics-port", 80, "Port for Prometheus metrics")
	root.PersistentFlags().BoolVar(&config.Enable, "metrics-enable", false, "Enable Prometheus metrics")

	config.pathPrefix = "/metrics"
}

func (config *ServerConfig) NewServer(ctx context.Context, shutdownTimeout, pollPeriod time.Duration, meters *[]Observable) *Server {
	mux := http.NewServeMux()
	mux.Handle(config.pathPrefix, promhttp.Handler())

	server := Server{
		http:             http.Server{Addr: fmt.Sprintf("%s:%d", config.host, config.port), Handler: mux},
		shutdownFinished: make(chan struct{}),
	}

	go poll(ctx, pollPeriod,
		func() {
			for _, observer := range *meters {
				observer()
			}
		},
	)

	go server.shutdown(ctx, shutdownTimeout)

	return &server
}
