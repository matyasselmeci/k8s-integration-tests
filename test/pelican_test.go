package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/random"
	// "github.com/gruntwork-io/terratest/modules/test_structure"
)

type pelicanFormatArgs struct {
	Tag         string
	CacheTag    string
	OriginTag   string
	DirectorTag string
	RegistryTag string
	ClientTag   string
}

var defaultPelicanFormatArgs pelicanFormatArgs = pelicanFormatArgs{
	Tag: "v7.27.0-rc.2",
}

// waitInitialDelay is the amount of time to wait for a deployment to become
// ready before polling it.  Needed because the readiness probes have an initial delay.
// The value is the sum of initialDelaySeconds + timeoutSeconds of the readiness tests.
var waitInitialDelay time.Duration = 35 * time.Second

// PelicanTestContext holds all setup components needed to run the Pelican tests, and provides a convenient way to pass them around
type PelicanTestContext struct {
	TestHandle
	logDir                string
	cancelCtx             context.CancelFunc
	secretsManifest       string
	formattedKustomizeDir string
	namespace             string
	kubectlOptions        *k8s.KubectlOptions
}

// subtestGetDataFromOrigin checks that the Pelican CLI tools in the dev pod
// can fetch data from the origin pod
func subtestGetDataFromOrigin(th TestHandle) {
	devPod := th.getPodNameByLabel("app.kubernetes.io/name=dev")
	// Check that condor_status filtered on the EP's name returns a non-empty string
	cmd := "pelican object get pelican://director:8444/public/data/0.0 /dev/null"
	th.waitUntilPodExecSucceedsSlice(devPod, "", strings.Split(cmd, " "), TWO_MINUTES, zeroExitCode)
}

// subtestMakeTestfiles creates a set of test files in the origin pod,
// which can be used to test the pelican object get command.
func subtestMakeTestfiles(th TestHandle) {
	originPod := th.getPodNameByLabel("app.kubernetes.io/name=origin")
	// The script to make test files has been mounted into the origin pod.
	// cmd := "bash /data/scripts/make_testfiles.sh /data/public/testfiles"
	cmd := "sleep 300"
	th.waitUntilPodExecSucceedsSlice(originPod, "", strings.Split(cmd, " "), TWO_MINUTES, zeroExitCode)
}

func setupPelicanTestSpace(t *testing.T) *PelicanTestContext {
	// -----------------------
	// Test environment setup
	// -----------------------

	// Define a test namespace name for the test
	namespace := "test-pelican-" + strings.ToLower(random.UniqueId())
	options := k8s.NewKubectlOptions("", "", namespace)
	th := TestHandle{t, options}

	// Create a logging dir for the test output, fail fast if we can't make it
	kustomizeDir := "../manifests/pelican"
	logDir := th.makeLogDir(kustomizeDir)

	// create k8s namespaces for the test
	k8s.CreateNamespace(t, options, namespace)

	// bind mount the origin's test data into minikube
	ctx, cancelCtx := context.WithCancel(context.Background())
	th.minikubeBindMount(ctx, "../data/pelican", "/data")

	// Create secrets for the pelican services: cert + signing keys
	// TODO OIDC secrets and web UI password are cargo culted from Brian A's repo, their values
	// have no meaning
	secretsManifest := th.applyPelicanSecrets(
		"Placeholder for the registry.",
		"Placeholder for the registry.",
		// Generated using `htpasswd -nbB -C 10 admin asdf`.
		"admin:$2y$10$ONeUS/VGwL9CoAD6pyZ2kusUjX8z0Sxuf8kz2g4PGbFb1GKUQ9J3C")

	// Template the kustomize dirs
	th.fillTemplateStructFromEnv(&defaultPelicanFormatArgs, "PELICAN_")
	formattedKustomizeDir := th.formatKustomizeDir(kustomizeDir, defaultPelicanFormatArgs)

	testContext := &PelicanTestContext{
		TestHandle:            th,
		logDir:                logDir,
		cancelCtx:             cancelCtx,
		secretsManifest:       secretsManifest,
		formattedKustomizeDir: formattedKustomizeDir,
		namespace:             namespace,
		kubectlOptions:        options,
	}

	return testContext
}

func startPelicanServices(tc *PelicanTestContext) {
	// Apply common resources
	k8s.KubectlApplyFromKustomize(tc.TestHandle.T, tc.kubectlOptions, filepath.Join(tc.formattedKustomizeDir, "common"))

	// Apply director; wait until it's up and ready. This blocks the other pieces.
	k8s.KubectlApplyFromKustomize(tc.TestHandle.T, tc.kubectlOptions, filepath.Join(tc.formattedKustomizeDir, "director"))
	time.Sleep(waitInitialDelay) // Give it time to start before we start polling.

	tc.waitUntilDeploymentsReady([]string{"director"}, TWO_MINUTES)

	// Apply registry; wait until it's up and ready. This blocks the cache and origin.
	k8s.KubectlApplyFromKustomize(tc.TestHandle.T, tc.kubectlOptions, filepath.Join(tc.formattedKustomizeDir, "registry"))
	time.Sleep(waitInitialDelay) // Give it time to start before we start polling.
	tc.waitUntilDeploymentsReady([]string{"registry"}, TWO_MINUTES)

	// Apply the rest.
	k8s.KubectlApplyFromKustomize(tc.TestHandle.T, tc.kubectlOptions, tc.formattedKustomizeDir)
	time.Sleep(waitInitialDelay) // Give it time to start before we start polling.

}

func cleanupPelicanTestSpace(setup *PelicanTestContext) {
	setup.dumpPodInformation(setup.logDir)
	noCleanup := os.Getenv("TEST_PELICAN_NO_CLEANUP") // TODO replace with test stages (https://terratest.gruntwork.io/docs/testing-best-practices/iterating-locally-using-test-stages/, https://pkg.go.dev/github.com/gruntwork-io/terratest@v0.56.0/modules/test-structure)
	if strings.ToLower(strings.TrimSpace(noCleanup)) == "true" {
		setup.T.Logf("TEST_PELICAN_NO_CLEANUP is set, skipping cleanup of test resources.")
	} else {
		setup.deletePelicanSecrets(setup.secretsManifest)
		k8s.KubectlDeleteFromKustomize(setup.T, setup.kubectlOptions, setup.formattedKustomizeDir)
		k8s.DeleteNamespace(setup.T, setup.kubectlOptions, setup.namespace)
		os.RemoveAll(setup.formattedKustomizeDir)
	}
	setup.cancelCtx()
}

func TestPelican(t *testing.T) {

	// -----------------------
	// Test environment setup
	// -----------------------

	testContext := setupPelicanTestSpace(t)

	// --------------------------
	// Test environment teardown
	// --------------------------

	// Cleanup runs all the reciprocal functions that delete created resources
	t.Cleanup(func() {
		cleanupPelicanTestSpace(testContext)
	})

	// --------------------------------
	// Test environment initialization
	// --------------------------------

	t.Run("Start pelican services", func(t *testing.T) {
		startPelicanServices(testContext)
	})

	if t.Failed() {
		return
	}

	// -------------
	// Actual tests
	// -------------

	// First test: Confirm that the kustomized resources pass their liveness/health checks
	t.Run("Confirm deployments become ready.", func(t *testing.T) {
		testContext.waitUntilAllDeploymentsReady(TWO_MINUTES)
	})

	if t.Failed() {
		return
	}

	t.Run("Create test files in origin pod", func(t *testing.T) {
		subtestMakeTestfiles(testContext.TestHandle)
	})

	// Ignore failure for now

	// Second test: Run a basic pelican object get
	t.Run("Confirm public `pelican object get` succeeds", func(t *testing.T) {
		subtestGetDataFromOrigin(testContext.TestHandle)
	})

}
