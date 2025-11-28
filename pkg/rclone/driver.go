package rclone

import (
	"context"
	"errors"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"k8s.io/klog"
)

var (
	DriverVersion = "SwissDataScienceCenter"
)

type DriverSetup func(csiDriver *csicommon.CSIDriver) (csi.ControllerServer, csi.NodeServer, error)

type DriverServe func(ctx context.Context) error

func Run(ctx context.Context, nodeID, endpoint *string, setup DriverSetup, serve DriverServe) error {
	driverName := os.Getenv("DRIVER_NAME")
	if driverName == "" {
		return errors.New("DRIVER_NAME env variable not set or empty")
	}
	klog.Infof("Starting new %s RcloneDriver in version %s", driverName, DriverVersion)

	driver := csicommon.NewCSIDriver(driverName, DriverVersion, *nodeID)
	driver.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
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
	s.Start(*endpoint, is, cs, ns)

	if err := serve(ctx); err != nil {
		return err
	}

	s.Wait()
	return nil
}
