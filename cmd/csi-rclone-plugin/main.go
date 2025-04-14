package main

import (
	"flag"
	"fmt"
	"os"
	"time"

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
)

func init() {
	flag.Set("logtostderr", "true")
}

func main() {

	root := &cobra.Command{
		Use:   "rclone",
		Short: "CSI based rclone driver",
	}

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Start the CSI driver.",
	}
	root.AddCommand(runCmd)

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

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Prints information about this version of csi rclone plugin",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("csi-rclone plugin Version: %s", rclone.DriverVersion)
		},
	}
	root.AddCommand(versionCmd)

	root.ParseFlags(os.Args[1:])
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
	d.WithNodeServer(ns)
	err = d.Run()
	if err != nil {
		panic(err)
	}
}

func handleController() {
	d := rclone.NewDriver(nodeID, endpoint)
	cs := rclone.NewControllerServer(d.CSIDriver)
	d.WithControllerServer(cs)
	err := d.Run()
	if err != nil {
		panic(err)
	}
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
