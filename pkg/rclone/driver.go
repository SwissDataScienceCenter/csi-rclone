package rclone

import (
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/SwissDataScienceCenter/csi-rclone/pkg/kube"
	"github.com/container-storage-interface/spec/lib/go/csi"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"k8s.io/klog"
	"k8s.io/utils/mount"

	utilexec "k8s.io/utils/exec"
)

type Driver struct {
	csiDriver *csicommon.CSIDriver
	endpoint  string

	ns     *nodeServer
	cap    []*csi.VolumeCapability_AccessMode
	cscap  []*csi.ControllerServiceCapability
	Server csicommon.NonBlockingGRPCServer
}

var (
	DriverVersion = "SwissDataScienceCenter"
)

func getFreePort() (port int, err error) {
	var a *net.TCPAddr
	if a, err = net.ResolveTCPAddr("tcp", "localhost:0"); err == nil {
		var l *net.TCPListener
		if l, err = net.ListenTCP("tcp", a); err == nil {
			defer l.Close()
			return l.Addr().(*net.TCPAddr).Port, nil
		}
	}
	return
}

func NewDriver(nodeID, endpoint string) *Driver {
	driverName := os.Getenv("DRIVER_NAME")
	if driverName == "" {
		panic("DriverName env var not set!")
	}
	klog.Infof("Starting new %s RcloneDriver in version %s", driverName, DriverVersion)

	d := &Driver{}
	d.endpoint = endpoint

	d.csiDriver = csicommon.NewCSIDriver(driverName, DriverVersion, nodeID)
	d.csiDriver.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
	})
	d.csiDriver.AddControllerServiceCapabilities(
		[]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		})

	return d
}

func (d *Driver) RunNodeService() error {
	kubeClient, err := kube.GetK8sClient()
	if err != nil {
		return err
	}

	rclonePort, err := getFreePort()
	if err != nil {
		return fmt.Errorf("Cannot get a free TCP port to run rclone")
	}
	rcloneOps := NewRclone(kubeClient, rclonePort)

	s := csicommon.NewNonBlockingGRPCServer()
	ns := &nodeServer{
		DefaultNodeServer: csicommon.NewDefaultNodeServer(d.csiDriver),
		mounter: &mount.SafeFormatAndMount{
			Interface: mount.New(""),
			Exec:      utilexec.New(),
		},
		RcloneOps: rcloneOps,
	}
	s.Start(
		d.endpoint,
		csicommon.NewDefaultIdentityServer(d.csiDriver),
		nil,
		ns,
	)
	d.Server = s
	d.ns = ns

	return rcloneOps.Run()
}

func (d *Driver) RunControllerService() error {
	s := csicommon.NewNonBlockingGRPCServer()
	s.Start(
		d.endpoint,
		csicommon.NewDefaultIdentityServer(d.csiDriver),
		&controllerServer{
			DefaultControllerServer: csicommon.NewDefaultControllerServer(d.csiDriver),
			active_volumes:          map[string]int64{},
			mutex:                   sync.RWMutex{},
		},
		nil,
	)
	d.Server = s
	s.Wait()
	return nil
}

func (d *Driver) Stop() error {
	var err error
	if d.ns != nil && d.ns.RcloneOps != nil {
		err = d.ns.RcloneOps.Cleanup()
	}
	if d.Server != nil {
		d.Server.Stop()
	}
	return err
}
