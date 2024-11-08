// The Controller(Server) is responsible for creating, deleting, attaching, and detaching volumes and snapshots.

package rclone

import (
	"os"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"

	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
)

const secretAnnotationName = "csi-rclone.dev/secretName"

type controllerServer struct {
	*csicommon.DefaultControllerServer
	RcloneOps      Operations
	active_volumes map[string]int64
	mutex          sync.RWMutex
}

func (cs *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ValidateVolumeCapabilities must be provided volume id")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ValidateVolumeCapabilities without capabilities")
	}

	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	if _, ok := cs.active_volumes[volId]; !ok {
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

// Attaching Volume
func (cs *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ControllerPublishVolume not implemented")
}

// Detaching Volume
func (cs *controllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ControllerUnpublishVolume not implemented")
}

// Provisioning Volumes
func (cs *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
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
	volSizeBytes := int64(req.GetCapacityRange().GetRequiredBytes())
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	if val, ok := cs.active_volumes[volumeName]; ok && val != volSizeBytes {
		return nil, status.Errorf(codes.AlreadyExists, "Volume operation already exists for volume %s", volumeName)
	}
	cs.active_volumes[volumeName] = volSizeBytes

	// See https://github.com/kubernetes-csi/external-provisioner/blob/v5.1.0/pkg/controller/controller.go#L75
	// on how parameters from the persistent volume are parsed
	// We have to pass the secret name and namespace into the context so that the node server can use them
	// The external provisioner uses the secret name and namespace but it does not pass them into the request,
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

// Delete Volume
func (cs *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "DeteleVolume must be provided volume id")
	}
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	delete(cs.active_volumes, volId)

	return &csi.DeleteVolumeResponse{}, nil
}

func (*controllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ControllerExpandVolume not implemented")
}

func (cs *controllerServer) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return &csi.ControllerGetVolumeResponse{Volume: &csi.Volume{
		VolumeId: req.VolumeId,
	}}, nil
}

func (cs *controllerServer) ControllerModifyVolume(ctx context.Context, req *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	return &csi.ControllerModifyVolumeResponse{}, nil
}

func saveRcloneConf(configData string) (string, error) {
	rcloneConf, err := os.CreateTemp("", "rclone.conf")
	if err != nil {
		return "", err
	}

	if _, err = rcloneConf.Write([]byte(configData)); err != nil {
		return "", err
	}

	if err = rcloneConf.Close(); err != nil {
		return "", err
	}
	return rcloneConf.Name(), nil
}
