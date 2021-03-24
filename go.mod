module github.com/eclipse-iofog/port-manager/v3

go 1.15

require (
	github.com/eclipse-iofog/iofog-go-sdk/v3 v3.0.0-20210315001729-4bfb68b2b2a6
	github.com/go-logr/logr v0.3.0
	github.com/gogo/protobuf v1.3.1 // indirect
	github.com/googleapis/gnostic v0.4.1 // indirect
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79 // indirect
	github.com/operator-framework/operator-sdk v0.10.0
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d // indirect
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0 // indirect
	k8s.io/api v0.19.4
	k8s.io/apimachinery v0.19.4
	k8s.io/client-go v11.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.6.4
)

// Pinned to kubernetes-1.13.4
replace (
	bitbucket.org/ww/goautoneg => github.com/munnerz/goautoneg v0.0.0-20120707110453-a547fc61f48d
	// For sigs.k8s.io/controller-runtime v0.6.4
	github.com/go-logr/logr => github.com/go-logr/logr v0.3.0
	github.com/go-logr/zapr => github.com/go-logr/zapr v0.3.0
	k8s.io/client-go => k8s.io/client-go v0.19.4
	github.com/googleapis/gnostic => github.com/googleapis/gnostic v0.4.1
)
