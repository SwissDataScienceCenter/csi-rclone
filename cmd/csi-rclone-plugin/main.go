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
	mountUtils "k8s.io/mount-utils"
)

var (
	endpoint  string
	nodeID    string
	cacheDir  string
	cacheSize string
	meters    []metrics.Observable
)

func init() {
	flag.Set("logtostderr", "true")
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
	nodeCommandLineParameters(runCmd)
	controllerCommandLineParameters(runCmd)

	root.AddCommand(runCmd)

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Prints information about this version of csi rclone plugin",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("csi-rclone plugin Version: %s", rclone.DriverVersion)
		},
	}
	root.AddCommand(versionCmd)

	root.ParseFlags(os.Args[1:])

	if metricsServerConfig.Enabled {
		// Gracefully exit the metrics background servers
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()

		metricsServer := metricsServerConfig.NewServer(ctx, &meters)
		go metricsServer.ListenAndServe()
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%s", err.Error())
		os.Exit(1)
	}

	os.Exit(0)
}

func handleNode() {
	err := unmountOldVols()
	if err != nil {
		klog.Warningf("There was an error when trying to unmount old volumes: %v", err)
	}
	d := rclone.NewDriver(nodeID, endpoint)
	ns, err := rclone.NewNodeServer(d.CSIDriver, cacheDir, cacheSize)
	if err != nil {
		panic(err)
	}
	meters = append(meters, ns.Metrics()...)
	d.WithNodeServer(ns)
	err = d.Run()
	if err != nil {
		panic(err)
	}
}

func nodeCommandLineParameters(runCmd *cobra.Command) {
	runNode := &cobra.Command{
		Use:   "node",
		Short: "Start the CSI driver node service - expected to run in a daemonset on every node.",
		Run: func(cmd *cobra.Command, args []string) {
			handleNode()
		},
	}
	runNode.PersistentFlags().StringVar(&nodeID, "nodeid", "", "node id")
	runNode.MarkPersistentFlagRequired("nodeid")
	runNode.PersistentFlags().StringVar(&endpoint, "endpoint", "", "CSI endpoint")
	runNode.MarkPersistentFlagRequired("endpoint")
	runNode.PersistentFlags().StringVar(&cacheDir, "cachedir", "", "cache dir")
	runNode.PersistentFlags().StringVar(&cacheSize, "cachesize", "", "cache size")
	runCmd.AddCommand(runNode)
}

func handleController() {
	d := rclone.NewDriver(nodeID, endpoint)
	cs := rclone.NewControllerServer(d.CSIDriver)
	meters = append(meters, cs.Metrics()...)
	d.WithControllerServer(cs)
	err := d.Run()
	if err != nil {
		panic(err)
	}
}

func controllerCommandLineParameters(runCmd *cobra.Command) {
	runController := &cobra.Command{
		Use:   "controller",
		Short: "Start the CSI driver controller.",
		Run: func(cmd *cobra.Command, args []string) {
			handleController()
		},
	}
	runController.PersistentFlags().StringVar(&nodeID, "nodeid", "", "node id")
	runController.MarkPersistentFlagRequired("nodeid")
	runController.PersistentFlags().StringVar(&endpoint, "endpoint", "", "CSI endpoint")
	runController.MarkPersistentFlagRequired("endpoint")
	runCmd.AddCommand(runController)
}

// unmountOldVols is used to unmount volumes after a restart on a node
func unmountOldVols() error {
	const mountType = "fuse.rclone"
	const unmountTimeout = time.Second * 5
	klog.Info("Checking for existing mounts")
	mounter := mountUtils.Mounter{}
	mounts, err := mounter.List()
	if err != nil {
		return err
	}
	for _, mount := range mounts {
		if mount.Type != mountType {
			continue
		}
		err := mounter.UnmountWithForce(mount.Path, unmountTimeout)
		if err != nil {
			klog.Warningf("Failed to unmount %s because of %v.", mount.Path, err)
			continue
		}
		klog.Infof("Sucessfully unmounted %s", mount.Path)
	}
	return nil
}
