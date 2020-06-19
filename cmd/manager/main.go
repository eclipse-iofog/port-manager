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
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/eclipse-iofog/port-manager/v2/internal/manager"
)

var log = logf.Log.WithName("port-manager")

func printVersion() {
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	log.Info(fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version))
}

const (
	userEmailEnv        = "IOFOG_USER_EMAIL"
	userPassEnv         = "IOFOG_USER_PASS"
	proxyImageEnv       = "PROXY_IMAGE"
	proxyAddressEnv     = "PROXY_ADDRESS"
	proxyServiceTypeEnv = "PROXY_SERVICE_TYPE"
	routerAddressEnv    = "ROUTER_ADDRESS"
)

type env struct {
	optional bool
	key      string
	value    string
}

func generateManagerOptions(namespace string, cfg *rest.Config) manager.Options {
	envs := map[string]env{
		userEmailEnv:        {key: userEmailEnv},
		userPassEnv:         {key: userPassEnv},
		routerAddressEnv:    {key: routerAddressEnv},
		proxyImageEnv:       {key: proxyImageEnv},
		proxyServiceTypeEnv: {key: proxyServiceTypeEnv},
		proxyAddressEnv:     {key: proxyAddressEnv, optional: true},
	}
	// Read env vars
	for _, env := range envs {
		env.value = os.Getenv(env.key)
		if env.value == "" && !env.optional {
			log.Error(nil, env.key+" env var not set")
			os.Exit(1)
		}
		// Store result for later
		envs[env.key] = env
	}
	return manager.Options{
		Namespace:            namespace,
		UserEmail:            envs[userEmailEnv].value,
		UserPass:             envs[userPassEnv].value,
		ProxyImage:           envs[proxyImageEnv].value,
		ProxyServiceType:     envs[proxyServiceTypeEnv].value,
		ProxyExternalAddress: envs[proxyAddressEnv].value,
		RouterAddress:        envs[routerAddressEnv].value,
		Config:               cfg,
	}
}

func generateManagers(opt manager.Options) (mgrs []*manager.Manager) {
	// No external address provided, Manager will create Proxy LoadBalancer and single Deployment
	if opt.ProxyExternalAddress == "" {
		mgr, err := manager.New(opt)
		handleErr(err, "")
		mgrs = append(mgrs, mgr)
	} else {
		// External address provided, Manager will create ClusterIP and Deployment for each protocol
		for _, protocol := range []string{"http", "tcp"} {
			mgr, err := manager.NewFiltered(opt, protocol)
			handleErr(err, "")
			mgrs = append(mgrs, mgr)
		}
	}
	return
}

func handleErr(err error, msg string) {
	if err != nil {
		log.Error(err, msg)
		os.Exit(1)
	}
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
	handleErr(err, "Failed to get watch namespace")

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	handleErr(err, "")

	// Become the leader before proceeding
	err = leader.Become(context.TODO(), "iofog-port-manager-lock")
	handleErr(err, "")

	// Create file for readiness probe
	r := ready.NewFileReady()
	err = r.Set()
	handleErr(err, "")
	defer func() {
		err = r.Unset()
		handleErr(err, "")
	}()

	// Generate options for manager instance
	opt := generateManagerOptions(namespace, cfg)

	// Instantiate Manager(s)
	mgrs := generateManagers(opt)

	// Run Managers
	for _, mgr := range mgrs {
		go mgr.Run()
	}

	// Wait forever
	switch {
	}
}
