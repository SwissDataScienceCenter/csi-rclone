package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
)

type Server struct {
	server  httpBgServer
	Watcher Watcher
}

func (server *Server) ListenAndServe() {
	server.server.ListenAndServe()
}

func (server *Server) Shutdown() {
	if server.Watcher.tickerStop != nil {
		server.Watcher.Stop()
	}
	server.server.Shutdown()
}

type ServerConfig struct {
	host       string
	port       int16
	pathPrefix string
	enable     bool
}

func (config *ServerConfig) CommandLineParameters(root *cobra.Command) {
	root.PersistentFlags().StringVar(&config.host, "metrics-host", "localhost", "Host name or ip address for Prometheus metrics")
	root.PersistentFlags().Int16Var(&config.port, "metrics-port", 80, "Port for Prometheus metrics")
	root.PersistentFlags().BoolVar(&config.enable, "metrics-enable", false, "Enable Prometheus metrics")

	config.pathPrefix = "/metrics"
}

func (config *ServerConfig) NewServer(meters []Observable, pollDelay, shutdownTimeout time.Duration) *Server {
	mux := http.NewServeMux()
	mux.Handle(config.pathPrefix, promhttp.Handler())

	server := Server{
		server: httpBgServer{
			server:           http.Server{Addr: fmt.Sprintf("%s:%d", config.host, config.port), Handler: mux},
			shutdownFinished: make(chan struct{}),
			shutdownTimeout:  shutdownTimeout,
		},
	}

	server.Watcher.Poll(
		func() {
			for _, observer := range meters {
				observer()
			}
		},
		pollDelay,
	)

	return &server
}

func (config *ServerConfig) IsEnabled() bool { return config.enable }
