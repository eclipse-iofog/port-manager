package manager

import (
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"k8s.io/client-go/rest"
)

type Manager struct {
	watchNamespace string
	config *rest.Config
}

func New(watchNamespace string, config *rest.Config) *Manager {
	return &Manager{
		watchNamespace: watchNamespace,
		config: config,
	}
}

func (mgr *Manager) Run() error {
	if _, err := k8sclient.New(mgr.config, k8sclient.Options{}); err != nil {
		return err
	}

	return nil
}