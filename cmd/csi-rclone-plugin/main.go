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
	"github.com/container-storage-interface/spec/lib/go/csi"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"github.com/spf13/cobra"
	"k8s.io/klog"
)

var (
	endpoint  string
	nodeID    string
	cacheDir  string
	cacheSize string
	meters    []metrics.Observable
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
	metricsServerConfig := metrics.ServerConfig{
		Host:            "localhost",
		Port:            9090,
		PathPrefix:      "/metrics",
		PollPeriod:      30 * time.Second,
		ShutdownTimeout: 5 * time.Second,
		Enabled:         false,
	}

	root := &cobra.Command{
		Use:   "rclone",
		Short: "CSI based rclone driver",
	}
	metricsServerConfig.CommandLineParameters(root)

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Start the CSI driver.",
	}
	exitOnError(NodeCommandLineParameters(runCmd, &meters, &nodeID, &endpoint, &cacheDir, &cacheSize))
	exitOnError(rclone.ControllerCommandLineParameters(runCmd, &meters, &nodeID, &endpoint))

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

func NodeCommandLineParameters(runCmd *cobra.Command, meters *[]metrics.Observable, nodeID, endpoint, cacheDir, cacheSize *string) error {
	runNode := &cobra.Command{
		Use:   "node",
		Short: "Start the CSI driver node service - expected to run in a daemonset on every node.",
		RunE: func(cmd *cobra.Command, args []string) error {
			//Pointers are passed by value, so we use a pointer to a pointer to be able to retrieve the allocated server in the
			//run closure
			var nsDoublePointer **rclone.NodeServer
			return rclone.Run(context.Background(), nodeID, endpoint,
				func(csiDriver *csicommon.CSIDriver) (csi.ControllerServer, csi.NodeServer, error) {
					ns, err := rclone.NewNodeServer(csiDriver, *cacheDir, *cacheSize)
					if err != nil {
						return nil, nil, err
					}
					*meters = append(*meters, ns.Metrics()...)
					*nsDoublePointer = ns
					return nil, ns, nil
				},
				func(ctx context.Context) error {
					return (*nsDoublePointer).Run(ctx)
				},
			)
		},
	}
	runNode.PersistentFlags().StringVar(nodeID, "nodeid", "", "node id")
	if err := runNode.MarkPersistentFlagRequired("nodeid"); err != nil {
		return err
	}
	runNode.PersistentFlags().StringVar(endpoint, "endpoint", "", "CSI endpoint")
	if err := runNode.MarkPersistentFlagRequired("endpoint"); err != nil {
		return err
	}
	runNode.PersistentFlags().StringVar(cacheDir, "cachedir", "", "cache dir")
	runNode.PersistentFlags().StringVar(cacheSize, "cachesize", "", "cache size")
	runCmd.AddCommand(runNode)
	return nil
}
