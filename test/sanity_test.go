package test

import (
	"context"
	"os"
	"testing"

	"github.com/SwissDataScienceCenter/csi-rclone/pkg/kube"
	"github.com/SwissDataScienceCenter/csi-rclone/pkg/rclone"
	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMyDriver(t *testing.T) {
	// Setup the full driver and its environment
	endpoint := "unix:///tmp/plugin/csi.sock"
	kubeClient, err := kube.GetK8sClient()
	if err != nil {
		panic(err)
	}
	driver := rclone.NewDriver("hostname", endpoint, kubeClient)
	go driver.Run()
	err = os.MkdirAll("/tmp/sanity/mount/", 0700)
	if err != nil {
		t.Fatal(err)
	}
	err = os.MkdirAll("/tmp/sanity/stage/", 0700)
	if err != nil {
		t.Fatal(err)
	}
	err = os.MkdirAll("/tmp/plugin/", 0700)
	if err != nil {
		t.Fatal(err)
	}

	mntDir, err := os.MkdirTemp("/tmp/sanity/mount/", "mount")
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(mntDir)
	//defer os.RemoveAll(mntDir)

	mntStageDir, err := os.MkdirTemp("/tmp/sanity/stage/", "stage")
	if err != nil {
		t.Fatal(err)
	}
	os.Getwd()
	os.RemoveAll(mntStageDir)
	//defer os.RemoveAll(mntStageDir)

	// create secret containing storage config for use in the test
	kubeClient.CoreV1().Secrets("csi-rclone").Create(context.Background(), &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pvc", Namespace: "csi-rclone"},
		StringData: map[string]string{
			"remote":     "my-s3",
			"remotePath": "giab/",
			"configData": `[my-s3]
type=s3
provider=AWS`},
		Type: "Opaque",
	}, metav1.CreateOptions{})

	cfg := sanity.NewTestConfig()
	cfg.Address = endpoint

	cfg.TargetPath = mntDir
	cfg.StagingPath = mntStageDir
	cfg.Address = endpoint
	// cfg.SecretsFile = "testdata/secrets.yaml"
	cfg.TestVolumeParameters = map[string]string{
		"csi.storage.k8s.io/pvc/namespace":                       "csi-rclone",
		"csi.storage.k8s.io/pvc/name":                            "test-pvc",
		"csi.storage.k8s.io/provisioner-secret-name":             "rclone-secret",
		"csi.storage.k8s.io/provisioner-secret-namespace":        "csi-rclone",
		"csi.storage.k8s.io/controller-publish-secret-name":      "rclone-secret",
		"csi.storage.k8s.io/controller-publish-secret-namespace": "csi-rclone",
		"csi.storage.k8s.io/node-publish-secret-name":            "rclone-secret",
		"csi.storage.k8s.io/node-publish-secret-namespace":       "csi-rclone",
	}
	sanity.Test(t, cfg)

	// sanity just completely kills the driver, leaking the rclone daemon, so we cleanup manually
	driver.RcloneOps.Cleanup()

	kubeClient.CoreV1().Secrets("csi-rclone").Delete(context.Background(), "test-pvc", metav1.DeleteOptions{})
}
