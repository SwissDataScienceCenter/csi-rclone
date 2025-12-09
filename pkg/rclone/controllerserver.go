// The Controller(Server) is responsible for creating, deleting, attaching, and detaching volumes and snapshots.

package rclone

import (
	"context"
	"sync"

	"github.com/SwissDataScienceCenter/csi-rclone/pkg/metrics"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"

	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
)

const secretAnnotationName = "csi-rclone.dev/secretName"

type ControllerServerConfig struct{ DriverConfig }

type ControllerServer struct {
	*csicommon.DefaultControllerServer
	activeVolumes map[string]int64
	mutex         *sync.RWMutex
}

func NewControllerServer(csiDriver *csicommon.CSIDriver) *ControllerServer {
	return &ControllerServer{
		DefaultControllerServer: csicommon.NewDefaultControllerServer(csiDriver),
		activeVolumes:           map[string]int64{},
		mutex:                   &sync.RWMutex{},
	}
}

func (cs *ControllerServer) metrics() []metrics.Observable {
	var meters []metrics.Observable

	meter := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "csi_rclone_active_volume_count",
		Help: "Number of active (Mounted) volumes.",
	})
	meters = append(meters,
		func() {
			cs.mutex.RLock()
			defer cs.mutex.RUnlock()
			meter.Set(float64(len(cs.activeVolumes)))
		},
	)
	prometheus.MustRegister(meter)

	return meters
}

func (config *ControllerServerConfig) CommandLineParameters(runCmd *cobra.Command, meters *[]metrics.Observable) error {
	runController := &cobra.Command{
		Use:   "controller",
		Short: "Start the CSI driver controller.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Run(context.Background(),
				&config.DriverConfig,
				func(csiDriver *csicommon.CSIDriver) (*ControllerServer, *NodeServer, error) {
					cs := NewControllerServer(csiDriver)
					*meters = append(*meters, cs.metrics()...)
					return cs, nil, nil
				},
				func(_ context.Context, cs *ControllerServer, ns *NodeServer) error { return nil },
			)
		},
	}
	if err := config.DriverConfig.CommandLineParameters(runController); err != nil {
		return err
	}

	runCmd.AddCommand(runController)
	return nil
}

func (cs *ControllerServer) ValidateVolumeCapabilities(_ context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ValidateVolumeCapabilities must be provided volume id")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ValidateVolumeCapabilities without capabilities")
	}

	cs.mutex.RLock()
	defer cs.mutex.RUnlock()
	if _, ok := cs.activeVolumes[volId]; !ok {
		return nil, status.Errorf(codes.NotFound, "Volume %s not found", volId)
	}
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.VolumeContext,
			VolumeCapabilities: req.VolumeCapabilities,
			Parameters:         req.Parameters,
		},
	}, nil
}

// ControllerPublishVolume Attaching Volume
func (cs *ControllerServer) ControllerPublishVolume(_ context.Context, _ *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ControllerPublishVolume not implemented")
}

// ControllerUnpublishVolume Detaching Volume
func (cs *ControllerServer) ControllerUnpublishVolume(_ context.Context, _ *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ControllerUnpublishVolume not implemented")
}

// CreateVolume Provisioning Volumes
func (cs *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.Infof("ControllerCreateVolume: called with args %+v", *req)
	volumeName := req.GetName()
	if len(volumeName) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume name must be provided")
	}

	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume without capabilities")
	}

	// we don't use the size as it makes no sense for rclone. but csi drivers should succeed if
	// called twice with the same capacity for the same volume and fail if called twice with
	// differing capacity, so we need to remember it
	volSizeBytes := req.GetCapacityRange().GetRequiredBytes()
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	if val, ok := cs.activeVolumes[volumeName]; ok && val != volSizeBytes {
		return nil, status.Errorf(codes.AlreadyExists, "Volume operation already exists for volume %s", volumeName)
	}
	cs.activeVolumes[volumeName] = volSizeBytes

	// See https://github.com/kubernetes-csi/external-provisioner/blob/v5.1.0/pkg/controller/controller.go#L75
	// on how parameters from the persistent volume are parsed
	// We have to pass the secret name and namespace into the context so that the node server can use them
	// The external provisioner uses the secret name and namespace, but it does not pass them into the request,
	// so we read the PVC here to extract them ourselves because we may need them in the node server for decoding secrets.
	pvcName, pvcNameFound := req.Parameters["csi.storage.k8s.io/pvc/name"]
	pvcNamespace, pvcNamespaceFound := req.Parameters["csi.storage.k8s.io/pvc/namespace"]
	if !pvcNameFound || !pvcNamespaceFound {
		return nil, status.Error(codes.FailedPrecondition, "The PVC name and/or namespace are not present in the create volume request parameters.")
	}
	volumeContext := map[string]string{}
	if len(req.GetSecrets()) > 0 {
		pvc, err := getPVC(ctx, pvcNamespace, pvcName)
		if err != nil {
			return nil, err
		}
		secretName, secretNameFound := pvc.Annotations[secretAnnotationName]
		if !secretNameFound {
			return nil, status.Error(codes.FailedPrecondition, "The secret name is not present in the PVC annotations.")
		}
		volumeContext["secretName"] = secretName
		volumeContext["secretNamespace"] = pvcNamespace
	} else {
		// This is here for compatibility reasons before this update the secret name was equal to the PVC
		volumeContext["secretName"] = pvcName
		volumeContext["secretNamespace"] = pvcNamespace
	}
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volumeName,
			VolumeContext: volumeContext,
		},
	}, nil

}

func (cs *ControllerServer) DeleteVolume(_ context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "DeleteVolume must be provided volume id")
	}
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	delete(cs.activeVolumes, volId)

	return &csi.DeleteVolumeResponse{}, nil
}

func (*ControllerServer) ControllerExpandVolume(_ context.Context, _ *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ControllerExpandVolume not implemented")
}

func (cs *ControllerServer) ControllerGetVolume(_ context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return &csi.ControllerGetVolumeResponse{Volume: &csi.Volume{
		VolumeId: req.VolumeId,
	}}, nil
}

func (cs *ControllerServer) ControllerModifyVolume(_ context.Context, _ *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	return &csi.ControllerModifyVolumeResponse{}, nil
}
