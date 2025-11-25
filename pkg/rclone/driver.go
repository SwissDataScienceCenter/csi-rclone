package rclone

import (
	"context"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"k8s.io/klog"
)

type Driver struct {
	CSIDriver *csicommon.CSIDriver
	endpoint  string

	ns     *nodeServer
	cs     *controllerServer
	cap    []*csi.VolumeCapability_AccessMode
	cscap  []*csi.ControllerServiceCapability
	server csicommon.NonBlockingGRPCServer
}

var (
	DriverVersion = "SwissDataScienceCenter"
)

func NewDriver(nodeID, endpoint string) *Driver {
	driverName := os.Getenv("DRIVER_NAME")
	if driverName == "" {
		panic("DriverName env var not set!")
	}
	klog.Infof("Starting new %s RcloneDriver in version %s", driverName, DriverVersion)

	d := &Driver{}
	d.endpoint = endpoint

	d.CSIDriver = csicommon.NewCSIDriver(driverName, DriverVersion, nodeID)
	d.CSIDriver.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
	})
	d.CSIDriver.AddControllerServiceCapabilities(
		[]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		})

	return d
}

func (d *Driver) WithNodeServer(ns *nodeServer) *Driver {
	d.ns = ns
	return d
}

func (d *Driver) WithControllerServer(cs *controllerServer) *Driver {
	d.cs = cs
	return d
}

func (d *Driver) Run() error {
	s := csicommon.NewNonBlockingGRPCServer()
	s.Start(
		d.endpoint,
		csicommon.NewDefaultIdentityServer(d.CSIDriver),
		d.cs,
		d.ns,
	)
	d.server = s
	if d.ns != nil && d.ns.RcloneOps != nil {
		onDaemonReady := func() error {
			if d.ns != nil {
				return d.ns.remountTrackedVolumes(context.Background())
			}
			return nil
		}
		return d.ns.RcloneOps.Run(onDaemonReady)
	}
	s.Wait()
	return nil
}

func (d *Driver) Stop() error {
	var err error
	if d.ns != nil && d.ns.RcloneOps != nil {
		err = d.ns.RcloneOps.Cleanup()
	}
	if d.server != nil {
		d.server.Stop()
	}
	return err
}
