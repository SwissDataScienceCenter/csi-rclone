package test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/SwissDataScienceCenter/csi-rclone/pkg/kube"
	"github.com/SwissDataScienceCenter/csi-rclone/pkg/rclone"
	"github.com/google/uuid"
	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
	"github.com/kubernetes-csi/csi-test/v5/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func getMountDirs() (string, string) {
	tmpDir := os.TempDir()
	uuid := uuid.New().String()
	mntDir := tmpDir + "mount-" + uuid
	stageDir := tmpDir + "stage-" + uuid
	return mntDir, stageDir
}

func createSocketDir() (string, error) {
	uuid := uuid.New().String()
	tmpDir := os.TempDir()
	socketDir := tmpDir + "socket-" + uuid
	os.RemoveAll(socketDir)
	err := os.MkdirAll(socketDir, 0700)
	if err != nil {
		return "", nil
	}
	return socketDir, nil
}

var _ = Describe("Sanity CSI checks", Ordered, func() {
	var err error
	var kubeClient *kubernetes.Clientset = &kubernetes.Clientset{}
	var endpoint string
	var driver *rclone.Driver = &rclone.Driver{}
	var socketDir string

	BeforeAll(func() {
		socketDir, err = createSocketDir()
		Expect(err).ShouldNot(HaveOccurred())
		endpoint = fmt.Sprintf("unix://%s/csi.sock", socketDir)
		kubeClient, err = kube.GetK8sClient()
		Expect(err).ShouldNot(HaveOccurred())
		os.Setenv("DRIVER_NAME", "csi-rclone")
		driver = rclone.NewDriver("hostname", endpoint, kubeClient)
		go func() {
			defer GinkgoRecover()
			err := driver.Run()
			Expect(err).ShouldNot(HaveOccurred())
		}()
		_, err = utils.Connect(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterAll(func() {
		driver.Stop()
		os.RemoveAll(socketDir)
		os.Unsetenv("DRIVER_NAME")
	})

	Context("Legacy setup without decryption", Ordered, func() {
		var cfg *sanity.TestConfig = &sanity.TestConfig{}
		var testCtx *sanity.TestContext = &sanity.TestContext{}

		BeforeEach(func() {
			mntDir, stageDir := getMountDirs()
			kubeClient.CoreV1().Secrets("csi-rclone").Create(context.Background(), &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc", Namespace: "csi-rclone"},
				StringData: map[string]string{
					"remote":     "my-s3",
					"remotePath": "giab/",
					"secretKey":  "cw_0x689RpI-jtRR7oE8h_eQsKImvJapLeSbXpwF4e4=",
					"configData": `[my-s3]
type=s3
provider=AWS`},
				Type: "Opaque",
			}, metav1.CreateOptions{})
			*cfg = sanity.NewTestConfig()
			cfg.TargetPath = mntDir
			cfg.StagingPath = stageDir
			cfg.Address = endpoint
			cfg.TestVolumeParameters = map[string]string{
				"csi.storage.k8s.io/pvc/namespace": "csi-rclone",
				"csi.storage.k8s.io/pvc/name":      "test-pvc",
			}
		})

		AfterEach(func() {
			kubeClient.CoreV1().Secrets("csi-rclone").Delete(context.Background(), "test-pvc", metav1.DeleteOptions{})
		})

		AfterAll(func() {
			testCtx.Finalize()
		})

		Describe("Execute the test", func() {
			testCtx = sanity.GinkgoTest(cfg)
		})
	})

	Context("Legacy setup with decryption", Ordered, func() {
		var cfg *sanity.TestConfig = &sanity.TestConfig{}
		var testCtx *sanity.TestContext = &sanity.TestContext{}

		BeforeEach(func() {
			mntDir, stageDir := getMountDirs()
			kubeClient.CoreV1().Secrets("csi-rclone").Create(context.Background(), &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc", Namespace: "csi-rclone"},
				StringData: map[string]string{
					"remote":     "my-s3",
					"remotePath": "giab/",
					"secretKey":  "cw_0x689RpI-jtRR7oE8h_eQsKImvJapLeSbXpwF4e4=",
					"configData": `[my-s3]
type=<sensitive>
provider=AWS`},
				Type: "Opaque",
			}, metav1.CreateOptions{})
			// create secret containing saved storage secrets. `type` which is `s3` is encrypted like a secret
			// if decryption fails, then the storage cannot be mounted
			kubeClient.CoreV1().Secrets("csi-rclone").Create(context.Background(), &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc-secrets", Namespace: "csi-rclone"},
				StringData: map[string]string{"type": "gAAAAABK-fBwYcjuQgctfZknI2ko2uLqj6DRzRa7kFTKnWm_nkjwGWGTai5eyhNXlp6_6QjeTC7B8IWvhBsvG1Q6Zk2eDYDVQg=="},
				Type:       "Opaque",
			}, metav1.CreateOptions{})

			*cfg = sanity.NewTestConfig()
			cfg.Address = endpoint
			cfg.TargetPath = mntDir
			cfg.StagingPath = stageDir
			cfg.TestVolumeParameters = map[string]string{
				"csi.storage.k8s.io/pvc/namespace": "csi-rclone",
				"csi.storage.k8s.io/pvc/name":      "test-pvc",
			}
		})

		AfterEach(func() {
			kubeClient.CoreV1().Secrets("csi-rclone").Delete(context.Background(), "test-pvc", metav1.DeleteOptions{})
			kubeClient.CoreV1().Secrets("csi-rclone").Delete(context.Background(), "test-pvc-secrets", metav1.DeleteOptions{})
		})

		AfterAll(func() {
			testCtx.Finalize()
		})

		Describe("Execute the test", func() {
			testCtx = sanity.GinkgoTest(cfg)
		})
	})

	Context("Secrets from annotations without decryption", Ordered, func() {
		var cfg *sanity.TestConfig = &sanity.TestConfig{}
		var testCtx *sanity.TestContext = &sanity.TestContext{}

		BeforeEach(func() {
			mntDir, stageDir := getMountDirs()
			*cfg = sanity.NewTestConfig()
			cfg.Address = endpoint
			cfg.SecretsFile = "testdata/secrets.yaml"
			cfg.TargetPath = mntDir
			cfg.StagingPath = stageDir
			cfg.TestVolumeParameters = map[string]string{
				"csi.storage.k8s.io/pvc/namespace":                 "csi-rclone",
				"csi.storage.k8s.io/pvc/name":                      "some-pvc",
				"csi.storage.k8s.io/node-publish-secret-namespace": "csi-rclone",
				"csi.storage.k8s.io/node-publish-secret-name":      "test-pvc",
			}
		})

		AfterAll(func() {
			testCtx.Finalize()
		})

		Describe("Execute the test", func() {
			testCtx = sanity.GinkgoTest(cfg)
		})
	})

	Context("Serets from annotations with decryption", Ordered, func() {
		var cfg *sanity.TestConfig = &sanity.TestConfig{}
		var testCtx *sanity.TestContext = &sanity.TestContext{}

		BeforeEach(func() {
			mntDir, stageDir := getMountDirs()
			kubeClient.CoreV1().Secrets("csi-rclone").Create(context.Background(), &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pvc-secrets", Namespace: "csi-rclone"},
				StringData: map[string]string{"type": "gAAAAABK-fBwYcjuQgctfZknI2ko2uLqj6DRzRa7kFTKnWm_nkjwGWGTai5eyhNXlp6_6QjeTC7B8IWvhBsvG1Q6Zk2eDYDVQg=="},
				Type:       "Opaque",
			}, metav1.CreateOptions{})

			*cfg = sanity.NewTestConfig()
			cfg.Address = endpoint
			cfg.SecretsFile = "testdata/secrets.yaml"
			cfg.TargetPath = mntDir
			cfg.StagingPath = stageDir
			cfg.TestVolumeParameters = map[string]string{
				"csi.storage.k8s.io/pvc/namespace":                 "csi-rclone",
				"csi.storage.k8s.io/pvc/name":                      "some-pvc",
				"csi.storage.k8s.io/node-publish-secret-namespace": "csi-rclone",
				"csi.storage.k8s.io/node-publish-secret-name":      "test-pvc",
			}
		})

		AfterEach(func() {
			kubeClient.CoreV1().Secrets("csi-rclone").Delete(context.Background(), "test-pvc-secrets", metav1.DeleteOptions{})
		})

		AfterAll(func() {
			testCtx.Finalize()
		})

		Describe("Execute the test", func() {
			testCtx = sanity.GinkgoTest(cfg)
		})
	})

})

func TestSanity(t *testing.T) {
	RegisterFailHandler(Fail)
	suiteConfig, reporterConfig := GinkgoConfiguration()
	suiteConfig.SkipStrings = []string{"NEVER-RUN"}
	reporterConfig.FullTrace = true
	RunSpecs(t, "Sanity tests", suiteConfig, reporterConfig)
}
