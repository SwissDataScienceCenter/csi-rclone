// Node(Server) takes charge of volume mounting and unmounting.

package rclone

// Restructure this file !!!
// Follow lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/SwissDataScienceCenter/csi-rclone/pkg/kube"
	"github.com/SwissDataScienceCenter/csi-rclone/pkg/metrics"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/fernet/fernet-go"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/ini.v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
	mountutils "k8s.io/mount-utils"
)

type NodeServer struct {
	*csicommon.DefaultNodeServer
	mounter   mountutils.Interface
	RcloneOps Operations

	// Track mounted volumes for automatic remounting
	mountedVolumes map[string]MountedVolume
	mutex          *sync.Mutex
	stateFile      string
}

// unmountOldVols is used to unmount volumes after a restart on a node
func unmountOldVols() error {
	const mountType = "fuse.rclone"
	const unmountTimeout = time.Second * 5
	klog.Info("Checking for existing mounts")
	mounter := mountutils.Mounter{}
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
			klog.Warningf("Failed to unmount %s because of %v.", mount.Path, err)
			continue
		}
		klog.Infof("Sucessfully unmounted %s", mount.Path)
	}
	return nil
}

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

func NewNodeServer(csiDriver *csicommon.CSIDriver, cacheDir string, cacheSize string) (*NodeServer, error) {
	err := unmountOldVols()
	if err != nil {
		klog.Warningf("There was an error when trying to unmount old volumes: %v", err)
		return nil, err
	}

	kubeClient, err := kube.GetK8sClient()
	if err != nil {
		return nil, err
	}

	rclonePort, err := getFreePort()
	if err != nil {
		return nil, fmt.Errorf("Cannot get a free TCP port to run rclone")
	}

	ns := &NodeServer{
		DefaultNodeServer: csicommon.NewDefaultNodeServer(csiDriver),
		mounter:           &mountutils.Mounter{},
		RcloneOps:         NewRclone(kubeClient, rclonePort, cacheDir, cacheSize),
		mountedVolumes:    make(map[string]MountedVolume),
		mutex:             &sync.Mutex{},
		stateFile:         "/run/csi-rclone/mounted_volumes.json",
	}

	// Ensure the folder exists
	if err = os.MkdirAll(filepath.Dir(ns.stateFile), 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %v", err)
	}

	// Load persisted state on startup
	ns.mutex.Lock()
	defer ns.mutex.Unlock()

	if ns.mountedVolumes, err = readVolumeMap(ns.stateFile); err != nil {
		klog.Warningf("Failed to load persisted volume state: %v", err)
	}

	return ns, nil
}

func (ns *NodeServer) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_UNKNOWN,
					},
				},
			},
		},
	}, nil
}

func (ns *NodeServer) Run(ctx context.Context) error {
	defer ns.Stop()
	return ns.RcloneOps.Run(ctx, func() error {
		return ns.remountTrackedVolumes(ctx)
	})
}

func (ns *NodeServer) metrics() []metrics.Observable {
	var meters []metrics.Observable

	meter := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "csi_rclone_active_volume_count",
		Help: "Number of active (Mounted) volumes.",
	})
	meters = append(meters,
		func() { meter.Set(float64(len(ns.mountedVolumes))) },
	)
	prometheus.MustRegister(meter)

	return meters
}

func (ns *NodeServer) Stop() {
	if err := ns.RcloneOps.Cleanup(); err != nil {
		klog.Errorf("Failed to cleanup rclone ops: %v", err)
	}
}

type NodeServerConfig struct {
	DriverConfig
	CacheDir  string
	CacheSize string
}

func (config *NodeServerConfig) CommandLineParameters(runCmd *cobra.Command, meters *[]metrics.Observable) error {
	runNode := &cobra.Command{
		Use:   "node",
		Short: "Start the CSI driver node service - expected to run in a daemonset on every node.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return Run(ctx, &config.DriverConfig,
				func(csiDriver *csicommon.CSIDriver) (*ControllerServer, *NodeServer, error) {
					ns, err := NewNodeServer(csiDriver, config.CacheDir, config.CacheSize)
					if err != nil {
						return nil, nil, err
					}
					*meters = append(*meters, ns.metrics()...)
					return nil, ns, err
				},
				func(ctx context.Context, cs *ControllerServer, ns *NodeServer) error {
					if ns == nil {
						return errors.New("node server uninitialized")
					}
					return ns.Run(ctx)
				},
			)
		},
	}
	if err := config.DriverConfig.CommandLineParameters(runNode); err != nil {
		return err
	}

	runNode.PersistentFlags().StringVar(&config.CacheDir, "cachedir", config.CacheDir, "cache dir")
	runNode.PersistentFlags().StringVar(&config.CacheSize, "cachesize", config.CacheSize, "cache size")

	runCmd.AddCommand(runNode)
	return nil
}

type MountedVolume struct {
	VolumeId        string            `json:"volume_id"`
	TargetPath      string            `json:"target_path"`
	Remote          string            `json:"remote"`
	RemotePath      string            `json:"remote_path"`
	ConfigData      string            `json:"config_data"`
	ReadOnly        bool              `json:"read_only"`
	Parameters      map[string]string `json:"parameters"`
	SecretName      string            `json:"secret_name"`
	SecretNamespace string            `json:"secret_namespace"`
}

// Mounting Volume (Preparation)
func validateNodeStageVolumeRequest(req *csi.NodeStageVolumeRequest) error {
	if req.GetVolumeId() == "" {
		return status.Error(codes.InvalidArgument, "empty volume id")
	}

	if req.GetStagingTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "empty staging path")
	}

	capability := req.GetVolumeCapability()
	if capability == nil {
		return status.Error(codes.InvalidArgument, "no volume capability set")
	}

	switch capability.GetAccessMode().GetMode() {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:
		return nil
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER:
		return nil
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:
		return nil
	case csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:
		return nil
	default:
		return status.Errorf(codes.FailedPrecondition, "Volume access mode not supported %v", capability.GetAccessMode().GetMode())
	}
}

func isNodeStageReqReadOnly(req *csi.NodeStageVolumeRequest) bool {
	switch req.GetVolumeCapability().GetAccessMode().GetMode() {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:
		return true
	case csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:
		return true
	default:
		return false
	}
}

func getVolumeConfig(ctx context.Context, req *csi.NodeStageVolumeRequest) (*MountedVolume, error) {
	volumeContext := req.GetVolumeContext()
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

	return &MountedVolume{
		VolumeId:        req.GetVolumeId(),
		TargetPath:      req.GetStagingTargetPath(),
		Remote:          remote,
		RemotePath:      remotePath,
		ConfigData:      configData,
		ReadOnly:        isNodeStageReqReadOnly(req),
		Parameters:      flags,
		SecretName:      secretName,
		SecretNamespace: secretNamespace,
	}, e
}

func (ns *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	klog.Infof("NodeStageVolume called with: %v", *req)

	if err := validateNodeStageVolumeRequest(req); err != nil {
		return nil, err
	}

	// Already staged ?
	if volume, ok := ns.mountedVolumes[req.GetVolumeId()]; ok {
		if volume.TargetPath == req.GetStagingTargetPath() && volume.ReadOnly == isNodeStageReqReadOnly(req) {
			return &csi.NodeStageVolumeResponse{}, nil
		}
		return nil, status.Error(codes.AlreadyExists, "Requested Volume capability incompatible with currently staged volume")
	}

	// Mount a tmpfs which is going to serve as a fixed point to allow rebinding fuse whenever necessary
	if err := ns.mounter.Mount("tmpfs", req.GetStagingTargetPath(), "tmpfs", []string{"size=1M"}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	volume, err := getVolumeConfig(ctx, req)
	if err != nil {
		return nil, err
	}

	rcloneVol := &RcloneVolume{
		ID:         volume.VolumeId,
		Remote:     volume.Remote,
		RemotePath: volume.RemotePath,
	}
	err = ns.RcloneOps.Mount(ctx, rcloneVol, volume.TargetPath, volume.ConfigData, volume.ReadOnly, volume.Parameters)
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
	ns.trackMountedVolume(volume)

	return &csi.NodeStageVolumeResponse{}, nil
}

func validateNodeUnstageVolumeRequest(req *csi.NodeUnstageVolumeRequest) error {
	if req.GetVolumeId() == "" {
		return status.Error(codes.InvalidArgument, "empty volume id")
	}
	if req.GetStagingTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "empty staging path")
	}

	return nil
}

func (ns *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.Infof("NodeUnstageVolume called with: %v", *req)

	if err := validateNodeUnstageVolumeRequest(req); err != nil {
		return nil, err
	}

	if volume, ok := ns.mountedVolumes[req.GetVolumeId()]; ok {
		if err := ns.RcloneOps.Unmount(ctx, volume.VolumeId, volume.TargetPath); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

		// Remove the volume from tracking
		ns.removeTrackedVolume(req.GetVolumeId())

		for {
			if err := ns.mounter.Unmount(volume.TargetPath); err != nil {
				// keep unmounting whatever is on the folder until we can't
				break
			}
		}
	} else {
		return nil, status.Error(codes.NotFound, "volume not found")
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func validateNodePublishVolumeRequest(req *csi.NodePublishVolumeRequest) error {
	if req.GetVolumeId() == "" {
		return status.Error(codes.InvalidArgument, "empty volume id")
	}

	if req.GetStagingTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "empty staging path")
	}

	if req.GetTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "empty target path")
	}

	capability := req.GetVolumeCapability()
	if capability == nil {
		return status.Error(codes.InvalidArgument, "no volume capability set")
	}

	switch capability.GetAccessMode().GetMode() {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:
		return nil
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER:
		return nil
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:
		return nil
	case csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:
		return nil
	default:
		return status.Errorf(codes.FailedPrecondition, "Volume access mode not supported %v", capability.GetAccessMode().GetMode())
	}
}

func (ns *NodeServer) NodePublishVolume(_ context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.Infof("NodePublishVolume called with: %v", *req)

	if err := validateNodePublishVolumeRequest(req); err != nil {
		return nil, err
	}

	volume, ok := ns.mountedVolumes[req.GetVolumeId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "Volume not found")
	}
	if volume.ReadOnly && !req.GetReadonly() {
		return nil, status.Error(codes.AlreadyExists, "Volume is already published")
	}

	if mounts, err := ns.mounter.GetMountRefs(req.GetTargetPath()); err == nil && len(mounts) > 0 {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	if err := os.MkdirAll(req.GetTargetPath(), 0755); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	//// Mount a tmpfs which is going to serve as a fixed point to allow rebinding fuse whenever necessary
	//if err := ns.mounter.Mount("tmpfs", req.GetTargetPath(), "tmpfs", []string{"size=1M"}); err != nil {
	//	return nil, status.Error(codes.Internal, err.Error())
	//}

	options := []string{"bind"}
	if req.GetReadonly() {
		options = append(options, "remount", "ro")
	}
	if err := ns.mounter.Mount(req.GetStagingTargetPath(), req.GetTargetPath(), "", options); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

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

func extractFlags(volumeContext map[string]string, secret map[string]string, pvcSecret *v1.Secret, savedPvcSecret *v1.Secret) (string, string, string, map[string]string, error) {

	// Empty argument list
	flags := make(map[string]string)

	// Secret values are default, gets merged and overriden by corresponding PV values
	if len(secret) > 0 {
		// Needs byte to string casting for map values
		for k, v := range secret {
			flags[k] = v
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
			savedSecrets[k] = string(fernet.VerifyAndDecrypt(v, 0, []*fernet.Key{fernetKey}))
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
func (ns *NodeServer) NodeUnpublishVolume(_ context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.Infof("NodeUnpublishVolume called with: %v", *req)

	if err := validateUnPublishVolumeRequest(req); err != nil {
		return nil, err
	}

	for {
		if err := ns.mounter.Unmount(req.GetTargetPath()); err != nil {
			// keep unmounting whatever is on the folder until we can't
			break
		}
	}

	if err := os.Remove(req.GetTargetPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, status.Error(codes.Internal, err.Error())
	}

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
func (*NodeServer) NodeExpandVolume(_ context.Context, _ *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method NodeExpandVolume not implemented")
}

// Track mounted volume for automatic remounting
func (ns *NodeServer) trackMountedVolume(volume *MountedVolume) {
	ns.mutex.Lock()
	defer ns.mutex.Unlock()

	ns.mountedVolumes[volume.VolumeId] = *volume
	klog.Infof("Tracked mounted volume %s at path %s", volume.VolumeId, volume.TargetPath)

	if err := writeVolumeMap(ns.stateFile, ns.mountedVolumes); err != nil {
		klog.Errorf("Failed to persist volume state: %v", err)
	}
}

// Remove tracked volume when unmounted
func (ns *NodeServer) removeTrackedVolume(volumeId string) {
	ns.mutex.Lock()
	defer ns.mutex.Unlock()

	delete(ns.mountedVolumes, volumeId)
	klog.Infof("Removed tracked volume %s", volumeId)

	if err := writeVolumeMap(ns.stateFile, ns.mountedVolumes); err != nil {
		klog.Errorf("Failed to persist volume state: %v", err)
	}
}

// Automatically remount all tracked volumes after daemon restart
func (ns *NodeServer) remountTrackedVolumes(ctx context.Context) error {
	type mountResult struct {
		volumeID string
		err      error
	}

	ns.mutex.Lock()
	defer ns.mutex.Unlock()

	volumesCount := len(ns.mountedVolumes)

	if volumesCount == 0 {
		klog.Info("No tracked volumes to remount")
		return nil
	}

	klog.Infof("Remounting %d tracked volumes", volumesCount)

	// Limit the number of active workers to the number of CPU threads (arbitrarily chosen)
	limits := make(chan bool, runtime.GOMAXPROCS(0))
	defer close(limits)

	results := make(chan mountResult, volumesCount)
	defer close(results)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	for volumeId, mv := range ns.mountedVolumes {
		go func() {
			limits <- true // block until there is a free slot in the queue
			defer func() {
				<-limits // free a slot in the queue when we exit
			}()

			ctxWithMountTimeout, cancel := context.WithTimeout(ctxWithTimeout, 30*time.Second)
			defer cancel()

			klog.Infof("Remounting volume %s to %s", volumeId, mv.TargetPath)

			// Create the mount directory if it doesn't exist
			var err error
			if err = os.MkdirAll(mv.TargetPath, 0750); err != nil {
				klog.Errorf("Failed to create mount directory %s: %v", mv.TargetPath, err)
			} else {
				// Remount the volume
				rcloneVol := &RcloneVolume{
					ID:         mv.VolumeId,
					Remote:     mv.Remote,
					RemotePath: mv.RemotePath,
				}

				err = ns.RcloneOps.Mount(ctxWithMountTimeout, rcloneVol, mv.TargetPath, mv.ConfigData, mv.ReadOnly, mv.Parameters)
			}

			results <- mountResult{volumeId, err}
		}()
	}

	for {
		select {
		case result := <-results:
			volumesCount--
			if result.err != nil {
				klog.Errorf("Failed to remount volume %s: %v", result.volumeID, result.err)
				// Don't return error here, continue with other volumes not to block all users because of a failed mount.
				delete(ns.mountedVolumes, result.volumeID)
				// Should we keep it on disk? This will be lost on the first new mount which will override the file.
			} else {
				klog.Infof("Successfully remounted volume %s", result.volumeID)
			}
			if volumesCount == 0 {
				return nil
			}
		case <-ctxWithTimeout.Done():
			return ctxWithTimeout.Err()
		}
	}
}

// Persist volume state to disk
func writeVolumeMap(filename string, volumes map[string]MountedVolume) error {
	if filename == "" {
		return nil
	}

	data, err := json.Marshal(volumes)
	if err != nil {
		return fmt.Errorf("failed to marshal volume state: %v", err)
	}

	if err := os.WriteFile(filename, data, 0600); err != nil {
		return fmt.Errorf("failed to write state file: %v", err)
	}

	klog.Infof("Persisted volume state to %s", filename)
	return nil
}

// Load volume state from disk
func readVolumeMap(filename string) (map[string]MountedVolume, error) {
	volumes := make(map[string]MountedVolume)

	if filename == "" {
		return volumes, nil
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			klog.Info("No persisted volume state found, starting fresh")
			return volumes, nil
		}
		return volumes, fmt.Errorf("failed to read state file: %v", err)
	}

	if err := json.Unmarshal(data, &volumes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal volume state: %v", err)
	}

	klog.Infof("Loaded %d tracked volumes from %s", len(volumes), filename)
	return volumes, nil
}
