# ioFog Port Manager

Port Manager is a component of the ioFog Kubernetes Control Plane. It is responsible for managing HTTP and TCP Proxy deployments and services to satisfy requirements specified by Public Ports created through the ioFog Controller API.

Port Manager is deployed automatically when using iofogctl >= 2.0.0.

**Only one instance of the port manager should run per namespace**

## Build from Source

Go 1.16+ is a prerequisite.

See all `make` commands by running:
```
make help
```

To build, go ahead and run:
```
make build
```

## Running Tests

Run project unit tests:
```
make test
```
