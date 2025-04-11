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

func NewNodeServer(csiDriver *csicommon.CSIDriver, cacheDir string, cacheSize string) (*nodeServer, error) {
	kubeClient, err := kube.GetK8sClient()
	if err != nil {
		return nil, err
	}

	rclonePort, err := getFreePort()
	if err != nil {
		return nil, fmt.Errorf("Cannot get a free TCP port to run rclone")
	}
	rcloneOps := NewRclone(kubeClient, rclonePort, cacheDir, cacheSize)

	return &nodeServer{
		DefaultNodeServer: csicommon.NewDefaultNodeServer(csiDriver),
		mounter: &mount.SafeFormatAndMount{
			Interface: mount.New(""),
			Exec:      utilexec.New(),
		},
		RcloneOps: rcloneOps,
	}, nil
}

func NewControllerServer(csiDriver *csicommon.CSIDriver) *controllerServer {
	return &controllerServer{
		DefaultControllerServer: csicommon.NewDefaultControllerServer(csiDriver),
		active_volumes:          map[string]int64{},
		mutex:                   sync.RWMutex{},
	}
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
		return d.ns.RcloneOps.Run()
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
