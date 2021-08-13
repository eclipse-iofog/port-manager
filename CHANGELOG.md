# Changelog

## [v3.0.0-beta1] - 13 August 2021

* Update go-sdk w/ base URL changes
* Use go v1.16

## [v3.0.0-alpha1] - 24 March 2021

* Standardize logging across whole manager package
* Use go v1.15

## [v2.0.0] - 2020-08-05

## Features

* Add support for Ingress by creating ClusterIP Services

## Bugs

* Capitalization of Public Port protocol
* Initialization of manager
* ClusterIP Traffic policy
* Typo in wait 

## [v2.0.0-beta3] - 2020-04-23

### Features

* Add support for encoded passwords
* Add retry go routine for registering Proxy address to Controller

### Bugs

* Update go-sdk with LB hostname fix
* Fix error in init func
* Check if Proxy Service exists before triggering address register
* Expand retry routine to get service ip 
* Update go-sdk module with error return fix

## [v2.0.0-beta2] - 2020-04-06

### Features

* Add support for optional env vars
* Add support for static IP and service type env vars

### Bugs

* Fix manager initialization
* No longer restart on failure
* Add retries to iofog client
* Fix namespace bug

## [v2.0.0-beta] - 2020-03-12

### Features

* Update iofog-go-sdk module to v2 
* Add support for TCP/HTTP2 protocols

## [v2.0.0-alpha] - 2020-03-10

First version

### Features

* Use new public ports API from Controller
* Use config file to provide router address to proxy
* Add env vars for skupper router address and proxy image
* Check for config changes before updating deployment
* Update ports from all microservices on each loop
* Change to one proxy for all microservices
* Add update proxy logic
* Add caching logic to manager
* Add iofog-go-sdk module and instantiate client
* Add initial project structure and application

### Bugs

* Pass Proxy service IP to Controller on creation
* Fix secret name in deletion
* Fix run logic when cache invalid
  
[Unreleased]: https://github.com/eclipse-iofog/port-manager/compare/v2.0.0-beta3..HEAD
[v2.0.0-beta2]: https://github.com/eclipse-iofog/port-manager/compare/v2.0.0-beta2..v2.0.0-beta3
[v2.0.0-beta2]: https://github.com/eclipse-iofog/port-manager/compare/v2.0.0-beta..v2.0.0-beta2
[v2.0.0-beta]: https://github.com/eclipse-iofog/port-manager/compare/v2.0.0-alpha..v2.0.0-beta
[v2.0.0-alpha]: https://github.com/eclipse-iofog/port-manager/tree/v2.0.0-alpha
