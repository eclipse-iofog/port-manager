package main

import (
	"os"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/eclipse-iofog/port-manager/v3/internal/manager"
)

var log = zap.New()

const (
	userEmailEnv        = "IOFOG_USER_EMAIL"
	userPassEnv         = "IOFOG_USER_PASS"
	proxyImageEnv       = "PROXY_IMAGE"
	httpProxyAddressEnv = "HTTP_PROXY_ADDRESS"
	tcpProxyAddressEnv  = "TCP_PROXY_ADDRESS"
	routerAddressEnv    = "ROUTER_ADDRESS"
)

type env struct {
	optional bool
	key      string
	value    string
}

func generateManagerOptions(namespace string, cfg *rest.Config) (opts []manager.Options) {
	envs := map[string]env{
		userEmailEnv:        {key: userEmailEnv},
		userPassEnv:         {key: userPassEnv},
		routerAddressEnv:    {key: routerAddressEnv},
		proxyImageEnv:       {key: proxyImageEnv},
		httpProxyAddressEnv: {key: httpProxyAddressEnv, optional: true},
		tcpProxyAddressEnv:  {key: tcpProxyAddressEnv, optional: true},
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

	opt := manager.Options{
		Namespace:            namespace,
		UserEmail:            envs[userEmailEnv].value,
		UserPass:             envs[userPassEnv].value,
		ProxyImage:           envs[proxyImageEnv].value,
		ProxyServiceType:     "LoadBalancer",
		ProxyExternalAddress: "",
		ProtocolFilter:       "",
		ProxyName:            "http-proxy", // TODO: Fix this default, e.g. iofogctl tests get svc name
		RouterAddress:        envs[routerAddressEnv].value,
		Config:               cfg,
	}
	opts = append(opts, opt)
	if envs[httpProxyAddressEnv].value != "" && envs[tcpProxyAddressEnv].value != "" {
		// Update first opt
		opts[0].ProxyServiceType = "ClusterIP"
		opts[0].ProtocolFilter = "http"
		opts[0].ProxyName = "http-proxy"
		opts[0].ProxyExternalAddress = envs[httpProxyAddressEnv].value
		// Create second opt
		opt.ProxyServiceType = "ClusterIP"
		opt.ProtocolFilter = "tcp"
		opt.ProxyName = "tcp-proxy"
		opt.ProxyExternalAddress = envs[tcpProxyAddressEnv].value
		opts = append(opts, opt)
	}
	return opts
}

func generateManagers(namespace string, cfg *rest.Config) (mgrs []*manager.Manager) {
	opts := generateManagerOptions(namespace, cfg)
	// No external address provided, Manager will create Proxy LoadBalancer and single Deployment
	for idx := range opts {
		opt := &opts[idx]
		mgr, err := manager.New(opt)
		handleErr(err, "")
		mgrs = append(mgrs, mgr)
	}
	return
}

func handleErr(err error, msg string) {
	if err != nil {
		log.Error(err, msg)
		os.Exit(1)
	}
}

// getWatchNamespace returns the Namespace the operator should be watching for changes
func getWatchNamespace() (ns string) {
	// WatchNamespaceEnvVar is the constant for env variable WATCH_NAMESPACE
	// which specifies the Namespace to watch.
	// An empty value means the operator is running with cluster scope.
	ns, _ = os.LookupEnv("WATCH_NAMESPACE")
	return
}

func main() {
	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	handleErr(err, "")

	// Instantiate Manager(s)
	mgrs := generateManagers(getWatchNamespace(), cfg)

	// Run Managers
	for _, mgr := range mgrs {
		go mgr.Run()
	}

	// Wait forever
	select {}
}
