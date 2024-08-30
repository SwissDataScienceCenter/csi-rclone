package rclone

import (
	"net"
	"os"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	"k8s.io/utils/mount"

	utilexec "k8s.io/utils/exec"
)

type Driver struct {
	csiDriver *csicommon.CSIDriver
	endpoint  string

	ns        *nodeServer
	cap       []*csi.VolumeCapability_AccessMode
	cscap     []*csi.ControllerServiceCapability
	RcloneOps Operations
	Server    csicommon.NonBlockingGRPCServer
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

func NewDriver(nodeID, endpoint string, kubeClient *kubernetes.Clientset) *Driver {
	driverName := os.Getenv("DRIVER_NAME")
	if driverName == "" {
		panic("DriverName env var not set!")
	}
	klog.Infof("Starting new %s RcloneDriver in version %s", driverName, DriverVersion)

	d := &Driver{}
	d.endpoint = endpoint
	rclonePort, err := getFreePort()
	if err != nil {
		panic("Cannot get a free TCP port to run rclone")
	}
	d.RcloneOps = NewRclone(kubeClient, rclonePort)

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

func NewNodeServer(d *Driver) *nodeServer {
	return &nodeServer{
		// Creating and passing the NewDefaultNodeServer is useless and unecessary
		DefaultNodeServer: csicommon.NewDefaultNodeServer(d.csiDriver),
		mounter: &mount.SafeFormatAndMount{
			Interface: mount.New(""),
			Exec:      utilexec.New(),
		},
		RcloneOps: d.RcloneOps,
	}
}

func NewControllerServer(d *Driver) *controllerServer {
	return &controllerServer{
		// Creating and passing the NewDefaultControllerServer is useless and unecessary
		DefaultControllerServer: csicommon.NewDefaultControllerServer(d.csiDriver),
		RcloneOps:               d.RcloneOps,
		active_volumes:          map[string]int64{},
		mutex:                   sync.RWMutex{},
	}
}

func (d *Driver) Run() error {
	s := csicommon.NewNonBlockingGRPCServer()
	s.Start(
		d.endpoint,
		csicommon.NewDefaultIdentityServer(d.csiDriver),
		NewControllerServer(d),
		NewNodeServer(d),
	)
	d.Server = s
	go s.Wait()
	err := d.RcloneOps.Run()
	if err != nil {
		return err
	}
	return nil
}

func (d *Driver) Stop() error {
	err := d.RcloneOps.Cleanup()
	if d.Server != nil {
		d.Server.Stop()
	}
	return err
}
