// Node(Server) takes charge of volume mounting and unmounting.

package rclone

// Restructure this file !!!
// Follow lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/ini.v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	"github.com/SwissDataScienceCenter/csi-rclone/pkg/kube"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/fernet/fernet-go"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/mount"

	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
)

const CSI_ANNOTATION_PREFIX = "csi-rclone.dev"
const pvcSecretNameAnnotation = CSI_ANNOTATION_PREFIX + "/secretName"

type nodeServer struct {
	*csicommon.DefaultNodeServer
	mounter   *mount.SafeFormatAndMount
	RcloneOps Operations

	// Track mounted volumes for automatic remounting
	mountedVolumes map[string]*MountedVolume
	mutex          sync.RWMutex
	stateFile      string
}

type MountedVolume struct {
	VolumeId       string
	TargetPath     string
	Remote         string
	RemotePath     string
	ConfigData     string
	ReadOnly       bool
	Parameters     map[string]string
	SecretName     string
	SecretNamespace string
}

// Mounting Volume (Preparation)
func (ns *nodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method NodeStageVolume not implemented")
}

func (ns *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method NodeUnstageVolume not implemented")
}

// Mounting Volume (Actual Mounting)
func (ns *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	if err := validatePublishVolumeRequest(req); err != nil {
		return nil, err
	}

	targetPath := req.GetTargetPath()
	volumeId := req.GetVolumeId()
	volumeContext := req.GetVolumeContext()
	readOnly := req.GetReadonly()
	secretName, foundSecret := volumeContext["secretName"]
	secretNamespace, foundSecretNamespace := volumeContext["secretNamespace"]
	// For backwards compatibility - prior to the change in #20 this field held the namespace
	if !foundSecretNamespace {
		secretNamespace, foundSecretNamespace = volumeContext["namespace"]
	}

	if !foundSecret || !foundSecretNamespace {
		return nil, fmt.Errorf("Cannot find the 'secretName', 'secretNamespace' and/or 'namespace' fields in the volume context. If you are not using automated provisioning you have to specify these values manually in spec.csi.volumeAttributes in your PersistentVolume manifest. If you are using automated provisioning and these values are not found report this as a bug to the developers.")
	}

	// This is here for compatiblity reasons
	// If the req.Secrets is empty it means that the old method of passing the secret is used
	// Where the secret is not automatically loaded and we have to read it here.
	var pvcSecret *v1.Secret = nil
	var err error
	if len(req.GetSecrets()) == 0 {
		pvcSecret, err = getSecret(ctx, secretNamespace, secretName)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}
	}

	// Regardless of whether the secret is passed via annotations or not we assume that the encrypted secret
	// name is the PVC name or annotation value + "-secrets". There is no way for the user to pass this value
	// to the CSI driver.
	savedSecretName := secretName + "-secrets"
	savedPvcSecret, err := getSecret(ctx, secretNamespace, savedSecretName)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	} else if apierrors.IsNotFound(err) {
		klog.Warningf("Cannot find saved secrets %s: %s", savedSecretName, err)
	}

	remote, remotePath, configData, flags, e := extractFlags(req.GetVolumeContext(), req.GetSecrets(), pvcSecret, savedPvcSecret)
	delete(flags, "secretName")
	delete(flags, "namespace")
	if e != nil {
		klog.Warningf("storage parameter error: %s", e)
		return nil, e
	}
	notMnt, err := ns.mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(targetPath, 0750); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			notMnt = true
		} else {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if !notMnt {
		// testing original mount point, make sure the mount link is valid
		if _, err := os.ReadDir(targetPath); err == nil {
			klog.Infof("already mounted to target %s", targetPath)
			return &csi.NodePublishVolumeResponse{}, nil
		}
		// todo: mount link is invalid, now unmount and remount later (built-in functionality)
		klog.Warningf("ReadDir %s failed with %v, unmount this directory", targetPath, err)

		if err := ns.mounter.Unmount(targetPath); err != nil {
			klog.Errorf("Unmount directory %s failed with %v", targetPath, err)
			return nil, err
		}
	}

	rcloneVol := &RcloneVolume{
		ID:         volumeId,
		Remote:     remote,
		RemotePath: remotePath,
	}
	err = ns.RcloneOps.Mount(ctx, rcloneVol, targetPath, configData, readOnly, flags)
	if err != nil {
		if os.IsPermission(err) {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		if strings.Contains(err.Error(), "invalid argument") {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Track the mounted volume for automatic remounting
	ns.trackMountedVolume(volumeId, targetPath, remote, remotePath, configData, readOnly, flags, secretName, secretNamespace)

	// err = ns.WaitForMountAvailable(targetPath)
	// if err != nil {
	// 	return nil, status.Error(codes.Internal, err.Error())
	// }
	return &csi.NodePublishVolumeResponse{}, nil
}

func getSecret(ctx context.Context, namespace, name string) (*v1.Secret, error) {
	cs, err := kube.GetK8sClient()
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		return nil, fmt.Errorf("Failed to read Secret with K8s client because namespace is blank")
	}
	if name == "" {
		return nil, fmt.Errorf("Failed to read Secret with K8s client because name is blank")
	}
	return cs.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
}

func getPVC(ctx context.Context, namespace, name string) (*v1.PersistentVolumeClaim, error) {
	cs, err := kube.GetK8sClient()
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		return nil, fmt.Errorf("Failed to read PVC with K8s client because namespace is blank")
	}
	if name == "" {
		return nil, fmt.Errorf("Failed to read PVC with K8s client because name is blank")
	}
	return cs.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
}

func validatePublishVolumeRequest(req *csi.NodePublishVolumeRequest) error {
	if req.GetVolumeId() == "" {
		return status.Error(codes.InvalidArgument, "empty volume id")
	}

	if req.GetTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "empty target path")
	}

	if req.GetVolumeCapability() == nil {
		return status.Error(codes.InvalidArgument, "no volume capability set")
	}
	return nil
}

func extractFlags(volumeContext map[string]string, secret map[string]string, pvcSecret *v1.Secret, savedPvcSecret *v1.Secret) (string, string, string, map[string]string, error) {

	// Empty argument list
	flags := make(map[string]string)

	// Secret values are default, gets merged and overriden by corresponding PV values
	if len(secret) > 0 {
		// Needs byte to string casting for map values
		for k, v := range secret {
			flags[k] = string(v)
		}
	} else {
		klog.Infof("No csi-rclone connection defaults secret found.")
	}

	if len(volumeContext) > 0 {
		for k, v := range volumeContext {
			if strings.HasPrefix(k, "storage.kubernetes.io/") {
				continue
			}
			flags[k] = v
		}
	}
	if pvcSecret != nil {
		if len(pvcSecret.Data) > 0 {
			for k, v := range pvcSecret.Data {
				flags[k] = string(v)
			}
		}
	}

	remote := flags["remote"]
	remotePath := flags["remotePath"]

	if remotePathSuffix, ok := flags["remotePathSuffix"]; ok {
		remotePath = remotePath + remotePathSuffix
		delete(flags, "remotePathSuffix")
	}

	configData, flags := extractConfigData(flags)

	if savedPvcSecret != nil {
		if savedSecrets, err := decryptSecrets(flags, savedPvcSecret); err != nil {
			klog.Errorf("cannot decode saved storage secrets: %s", err)
		} else {
			if modifiedConfigData, err := updateConfigData(remote, configData, savedSecrets); err == nil {
				configData = modifiedConfigData
			} else {
				klog.Errorf("cannot update config data: %s", err)
			}
		}
	}

	return remote, remotePath, configData, flags, nil
}

func decryptSecrets(flags map[string]string, savedPvcSecret *v1.Secret) (map[string]string, error) {
	savedSecrets := make(map[string]string)

	userSecretKey, ok := flags["secretKey"]
	if !ok {
		return savedSecrets, status.Error(codes.InvalidArgument, "missing user secret key")
	}
	fernetKey, err := fernet.DecodeKey(userSecretKey)
	if err != nil {
		return savedSecrets, status.Errorf(codes.InvalidArgument, "cannot decode user secret key: %s", err)
	}

	if len(savedPvcSecret.Data) > 0 {
		for k, v := range savedPvcSecret.Data {
			savedSecrets[k] = string(fernet.VerifyAndDecrypt([]byte(v), 0, []*fernet.Key{fernetKey}))
		}
	}

	return savedSecrets, nil
}

func updateConfigData(remote string, configData string, savedSecrets map[string]string) (string, error) {
	iniData, err := ini.Load([]byte(configData))
	if err != nil {
		return "", fmt.Errorf("cannot load ini config data: %s", err)
	}

	section := iniData.Section(remote)
	for k, v := range savedSecrets {
		section.Key(k).SetValue(v)
	}

	buf := new(bytes.Buffer)
	iniData.WriteTo(buf)

	return buf.String(), nil
}

func extractConfigData(parameters map[string]string) (string, map[string]string) {
	flags := make(map[string]string)
	for k, v := range parameters {
		flags[k] = v
	}
	var configData string
	var ok bool
	if configData, ok = flags["configData"]; ok {
		delete(flags, "configData")
	}

	delete(flags, "remote")
	delete(flags, "remotePath")

	return configData, flags
}

// Unmounting Volumes
func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.Infof("NodeUnpublishVolume called with: %s", req)
	if err := validateUnPublishVolumeRequest(req); err != nil {
		return nil, err
	}
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		klog.Warning("no target path provided for NodeUnpublishVolume")
		return nil, status.Error(codes.InvalidArgument, "NodeUnpublishVolume Target Path must be provided")
	}

	if _, err := ns.RcloneOps.GetVolumeById(ctx, req.GetVolumeId()); err == ErrVolumeNotFound {
		klog.Warning("VolumeId not found for NodeUnpublishVolume")
		mount.CleanupMountPoint(req.GetTargetPath(), ns.mounter, false)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	if err := ns.RcloneOps.Unmount(ctx, req.GetVolumeId(), targetPath); err != nil {
		klog.Warningf("Unmounting volume failed: %s", err)
	}

	// Remove the volume from tracking
	ns.removeTrackedVolume(req.GetVolumeId())

	mount.CleanupMountPoint(req.GetTargetPath(), ns.mounter, false)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func validateUnPublishVolumeRequest(req *csi.NodeUnpublishVolumeRequest) error {
	if req.GetVolumeId() == "" {
		return status.Error(codes.InvalidArgument, "empty volume id")
	}

	if req.GetTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "empty target path")
	}

	return nil
}

// Resizing Volume
func (*nodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method NodeExpandVolume not implemented")
}

// Track mounted volume for automatic remounting
func (ns *nodeServer) trackMountedVolume(volumeId, targetPath, remote, remotePath, configData string, readOnly bool, parameters map[string]string, secretName, secretNamespace string) {
	ns.mutex.Lock()
	defer ns.mutex.Unlock()

	ns.mountedVolumes[volumeId] = &MountedVolume{
		VolumeId:       volumeId,
		TargetPath:     targetPath,
		Remote:         remote,
		RemotePath:     remotePath,
		ConfigData:     configData,
		ReadOnly:       readOnly,
		Parameters:     parameters,
		SecretName:     secretName,
		SecretNamespace: secretNamespace,
	}
	klog.Infof("Tracked mounted volume %s at path %s", volumeId, targetPath)

	if err := ns.persistState(); err != nil {
		klog.Errorf("Failed to persist volume state: %v", err)
	}
}

// Remove tracked volume when unmounted
func (ns *nodeServer) removeTrackedVolume(volumeId string) {
	ns.mutex.Lock()
	defer ns.mutex.Unlock()

	delete(ns.mountedVolumes, volumeId)
	klog.Infof("Removed tracked volume %s", volumeId)

	if err := ns.persistState(); err != nil {
		klog.Errorf("Failed to persist volume state: %v", err)
	}
}

// Automatically remount all tracked volumes after daemon restart
func (ns *nodeServer) remountTrackedVolumes(ctx context.Context) error {
	ns.mutex.RLock()
	defer ns.mutex.RUnlock()

	if len(ns.mountedVolumes) == 0 {
		klog.Info("No tracked volumes to remount")
		return nil
	}

	klog.Infof("Remounting %d tracked volumes", len(ns.mountedVolumes))

	for volumeId, mv := range ns.mountedVolumes {
		klog.Infof("Remounting volume %s to %s", volumeId, mv.TargetPath)

		// Create the mount directory if it doesn't exist
		if err := os.MkdirAll(mv.TargetPath, 0750); err != nil {
			klog.Errorf("Failed to create mount directory %s: %v", mv.TargetPath, err)
			continue
		}

		// Remount the volume
		rcloneVol := &RcloneVolume{
			ID:         mv.VolumeId,
			Remote:     mv.Remote,
			RemotePath: mv.RemotePath,
		}

		err := ns.RcloneOps.Mount(ctx, rcloneVol, mv.TargetPath, mv.ConfigData, mv.ReadOnly, mv.Parameters)
		if err != nil {
			klog.Errorf("Failed to remount volume %s: %v", volumeId, err)
			// Don't return error here - continue with other volumes
		} else {
			klog.Infof("Successfully remounted volume %s", volumeId)
		}
	}

	return nil
}

func (ns *nodeServer) WaitForMountAvailable(mountpoint string) error {
	for {
		select {
		case <-time.After(100 * time.Millisecond):
			notMnt, _ := ns.mounter.IsLikelyNotMountPoint(mountpoint)
			if !notMnt {
				return nil
			}
		case <-time.After(3 * time.Second):
			return errors.New("wait for mount available timeout")
		}
	}
}

// Persist volume state to disk
func (ns *nodeServer) persistState() error {
	ns.mutex.RLock()
	defer ns.mutex.RUnlock()

	if ns.stateFile == "" {
		return nil
	}

	data, err := json.Marshal(ns.mountedVolumes)
	if err != nil {
		return fmt.Errorf("failed to marshal volume state: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(ns.stateFile), 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %v", err)
	}

	if err := os.WriteFile(ns.stateFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write state file: %v", err)
	}

	klog.Infof("Persisted volume state to %s", ns.stateFile)
	return nil
}

// Load volume state from disk
func (ns *nodeServer) loadState() error {
	ns.mutex.Lock()
	defer ns.mutex.Unlock()

	if ns.stateFile == "" {
		return nil
	}

	data, err := os.ReadFile(ns.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			klog.Info("No persisted volume state found, starting fresh")
			return nil
		}
		return fmt.Errorf("failed to read state file: %v", err)
	}

	var volumes map[string]*MountedVolume
	if err := json.Unmarshal(data, &volumes); err != nil {
		return fmt.Errorf("failed to unmarshal volume state: %v", err)
	}

	ns.mountedVolumes = volumes
	klog.Infof("Loaded %d tracked volumes from %s", len(ns.mountedVolumes), ns.stateFile)
	return nil
}
