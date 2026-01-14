package rclone

import (
	"context"
	"errors"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"github.com/spf13/cobra"
	"k8s.io/klog"
)

const DriverVersion = "SwissDataScienceCenter"

type DriverSetup func(csiDriver *csicommon.CSIDriver) (*ControllerServer, *NodeServer, error)

type DriverServe func(ctx context.Context, cs *ControllerServer, ns *NodeServer) error

type DriverConfig struct {
	Endpoint string
	NodeID   string
}

func (config *DriverConfig) CommandLineParameters(runCmd *cobra.Command) error {
	runCmd.PersistentFlags().StringVar(&config.NodeID, "nodeid", config.NodeID, "node id")
	if err := runCmd.MarkPersistentFlagRequired("nodeid"); err != nil {
		return err
	}
	runCmd.PersistentFlags().StringVar(&config.Endpoint, "endpoint", config.Endpoint, "CSI endpoint")
	if err := runCmd.MarkPersistentFlagRequired("endpoint"); err != nil {
		return err
	}
	return nil
}

func Run(ctx context.Context, config *DriverConfig, setup DriverSetup, serve DriverServe) error {
	driverName := os.Getenv("DRIVER_NAME")
	if driverName == "" {
		return errors.New("DRIVER_NAME env variable not set or empty")
	}
	klog.Infof("Starting new %s RcloneDriver in version %s", driverName, DriverVersion)

	driver := csicommon.NewCSIDriver(driverName, DriverVersion, config.NodeID)
	driver.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
	})
	driver.AddControllerServiceCapabilities(
		[]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		})

	is := csicommon.NewDefaultIdentityServer(driver)
	cs, ns, setupErr := setup(driver)
	if setupErr != nil {
		return setupErr
	}

	s := csicommon.NewNonBlockingGRPCServer()
	defer s.Stop()
	s.Start(config.Endpoint, is, cs, ns)

	if err := serve(ctx, cs, ns); err != nil {
		return err
	}

	s.Wait()
	return nil
}
