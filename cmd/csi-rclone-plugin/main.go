package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/SwissDataScienceCenter/csi-rclone/pkg/kube"
	"github.com/SwissDataScienceCenter/csi-rclone/pkg/rclone"
	"github.com/spf13/cobra"
	"k8s.io/klog"
	mountUtils "k8s.io/mount-utils"
)

var (
	endpoint string
	nodeID   string
)

func init() {
	flag.Set("logtostderr", "true")
}

func main() {

	cmd := &cobra.Command{
		Use:   "rclone",
		Short: "CSI based rclone driver",
	}

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Start the CSI driver.",
		Run: func(cmd *cobra.Command, args []string) {
			handle()
		},
	}
	cmd.AddCommand(runCmd)

	runCmd.PersistentFlags().StringVar(&nodeID, "nodeid", "", "node id")
	runCmd.MarkPersistentFlagRequired("nodeid")

	runCmd.PersistentFlags().StringVar(&endpoint, "endpoint", "", "CSI endpoint")
	runCmd.MarkPersistentFlagRequired("endpoint")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Prints information about this version of csi rclone plugin",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf(`csi-rclone plugin
Version:    %s
`, rclone.DriverVersion)
		},
	}
	cmd.AddCommand(versionCmd)

	cmd.ParseFlags(os.Args[1:])
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%s", err.Error())
		os.Exit(1)
	}

	os.Exit(0)
}

func handle() {
	kubeClient, err := kube.GetK8sClient()
	if err != nil {
		panic(err)
	}
	err = unmountOldVols()
	if err != nil {
		klog.Warningf("There was an error when trying to unmount old volumes: %e", err)
	}
	d := rclone.NewDriver(nodeID, endpoint, kubeClient)
	err = d.Run()
	if err != nil {
		panic(err)
	}
}

const mountType = "fuse.rclone"
const unmountTimeout = time.Second * 5

func unmountOldVols() error {
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
			klog.Warningf("Failed to unmount %s because of %e, will try to unmount with force.", mount.Path, err)
			continue
		}
		klog.Infof("Sucessfully unmounted %s", mount.Path)
	}
	return nil
}
