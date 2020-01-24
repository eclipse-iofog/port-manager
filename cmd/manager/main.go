package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/operator-framework/operator-sdk/pkg/leader"
	"github.com/operator-framework/operator-sdk/pkg/ready"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/eclipse-iofog/port-manager/internal/manager"
)

var log = logf.Log.WithName("port-manager")

func printVersion() {
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	log.Info(fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version))
}

func main() {
	flag.Parse()

	// The logger instantiated here can be changed to any logger
	// implementing the logr.Logger interface. This logger will
	// be propagated through the whole operator, generating
	// uniform and structured logs.
	logf.SetLogger(logf.ZapLogger(false))

	printVersion()

	// Get namespace from environment variable
	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		log.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}

	// Get Controller access token from environment variable
	envs := []string{
		"IOFOG_USER_EMAIL",
		"IOFOG_USER_PASS",
		"PROXY_IMAGE",
		"ROUTER_ADDRESS",
	}
	for idx, envVar := range envs {
		env := os.Getenv(envVar)
		if env == "" {
			log.Error(nil, envVar+" env var not set")
			os.Exit(1)
		}
		// Store result for later
		envs[idx] = env
	}

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Become the leader before proceeding
	err = leader.Become(context.TODO(), "iofog-port-manager-lock")
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Create file for readiness probe
	r := ready.NewFileReady()
	err = r.Set()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}
	defer func() {
		if err = r.Unset(); err != nil {
			log.Error(err, "")
			os.Exit(1)
		}
	}()

	// Run
	if err = manager.New(namespace, envs[0], envs[1], envs[2], envs[3], cfg).Run(); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}
}
