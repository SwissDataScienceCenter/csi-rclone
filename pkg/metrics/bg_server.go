package metrics

import (
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/net/context"
	"k8s.io/klog"
)

type httpBgServer struct {
	server           http.Server
	shutdownFinished chan struct{}
	shutdownTimeout  time.Duration
}

func (bgServer *httpBgServer) ListenAndServe() {
	err := bgServer.server.ListenAndServe()

	if errors.Is(err, http.ErrServerClosed) {
		// Expected in case of shutdown.
		err = nil
	} else {
		klog.Warning("HTTP server failed: " + err.Error())
	}
	<-bgServer.shutdownFinished
}

func (bgServer *httpBgServer) Shutdown() {
	var waiter = make(chan os.Signal, 1) // buffered channel
	signal.Notify(waiter, syscall.SIGTERM, syscall.SIGINT)

	// blocks here until there's a signal
	<-waiter

	ctx, cancel := context.WithTimeout(context.Background(), bgServer.shutdownTimeout)
	defer cancel()

	err := bgServer.server.Shutdown(ctx)
	if err != nil {
		klog.Warning("Error while shutting down HTTP server: " + err.Error())
	} else {
		klog.Info("HTTP server successfully shut down.")
	}
	close(bgServer.shutdownFinished)
}
