package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SwissDataScienceCenter/csi-rclone/pkg/metrics"
	"github.com/SwissDataScienceCenter/csi-rclone/pkg/rclone"
	"github.com/spf13/cobra"
	"k8s.io/klog"
)

func exitOnError(err error) {
	if err != nil {
		klog.Error(err.Error())
		os.Exit(1)
	}
}

func init() {
	exitOnError(flag.Set("logtostderr", "true"))
}

func main() {
	var meters []metrics.Observable
	metricsServerConfig := metrics.ServerConfig{
		Host:            "localhost",
		Port:            9090,
		PathPrefix:      "/metrics",
		PollPeriod:      30 * time.Second,
		ShutdownTimeout: 5 * time.Second,
		Enabled:         false,
	}
	nodeServerConfig := rclone.NodeServerConfig{}
	controllerServerConfig := rclone.ControllerServerConfig{}

	root := &cobra.Command{
		Use:   "rclone",
		Short: "CSI based rclone driver",
	}
	metricsServerConfig.CommandLineParameters(root)

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Start the CSI driver.",
	}
	exitOnError(nodeServerConfig.CommandLineParameters(runCmd, &meters))
	exitOnError(controllerServerConfig.CommandLineParameters(runCmd, &meters))

	root.AddCommand(runCmd)

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Prints information about this version of csi rclone plugin",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("csi-rclone plugin Version: %s", rclone.DriverVersion)
		},
	}
	root.AddCommand(versionCmd)

	exitOnError(root.ParseFlags(os.Args[1:]))

	if metricsServerConfig.Enabled {
		// Gracefully exit the metrics background servers
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()

		metricsServer := metricsServerConfig.NewServer(ctx, &meters)
		go metricsServer.ListenAndServe()
	}

	exitOnError(root.Execute())

	os.Exit(0)
}
