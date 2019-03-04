// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// +build clusterchecks
// +build kubeapiserver

package providers

import (
	"fmt"
	"strings"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/DataDog/datadog-agent/pkg/autodiscovery/integration"
	"github.com/DataDog/datadog-agent/pkg/config"
	"github.com/DataDog/datadog-agent/pkg/util/kubernetes/apiserver"
	"github.com/DataDog/datadog-agent/pkg/util/log"
)

const (
	// AD on the load-balanced service IPs
	kubeServiceAnnotationPrefix = "ad.datadoghq.com/service."
	// AD on the individual service endpoints (TODO)
	kubeEndpointAnnotationPrefix = "ad.datadoghq.com/endpoints."
	// kubeEndpointIDPrefix         = "kube_endpoint://"
)

// KubeletConfigProvider implements the ConfigProvider interface for the kubelet.
type KubeServiceConfigProvider struct {
	lister   listersv1.ServiceLister
	upToDate bool
}

// KubeletConfigProvider implements the ConfigProvider interface for the kubelet.
type KubeEndpointConfigProvider struct {
	lister   listersv1.EndpointsLister
	upToDate bool
}

// NewKubeServiceConfigProvider returns a new ConfigProvider connected to kubelet.
// Connectivity is not checked at this stage to allow for retries, Collect will do it.
func NewKubeServiceConfigProvider(config config.ConfigurationProviders) (ConfigProvider, error) {
	ac, err := apiserver.GetAPIClient()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to apiserver: %s", err)
	}
	servicesInformer := ac.InformerFactory.Core().V1().Services()
	if servicesInformer == nil {
		return nil, fmt.Errorf("cannot get service informer: %s", err)
	}

	p := &KubeServiceConfigProvider{
		lister: servicesInformer.Lister(),
	}

	servicesInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.invalidate,
		UpdateFunc: p.invalidateIfChanged,
		DeleteFunc: p.invalidate,
	})

	return p, nil
}

// String returns a string representation of the KubeServiceConfigProvider
func (k *KubeServiceConfigProvider) String() string {
	return KubeServices
}

// Collect retrieves services from the apiserver, builds Config objects and returns them
func (k *KubeServiceConfigProvider) Collect() ([]integration.Config, error) {
	services, err := k.lister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	k.upToDate = true

	return parseServiceAnnotations(services)
}

// IsUpToDate allows to cache configs as long as no changes are detected in the apiserver
func (k *KubeServiceConfigProvider) IsUpToDate() (bool, error) {
	return k.upToDate, nil
}

func (k *KubeServiceConfigProvider) invalidate(obj interface{}) {
	if obj != nil {
		log.Trace("Invalidating configs on new/deleted service")
		k.upToDate = false
	}
}

func (k *KubeServiceConfigProvider) invalidateIfChanged(old, obj interface{}) {
	// Cast the updated object, don't invalidate on casting error.
	// nil pointers are safely handled by the casting logic.
	castedObj, ok := obj.(*v1.Service)
	if !ok {
		log.Errorf("Expected a Service type, got: %v", obj)
		return
	}
	// Cast the old object, invalidate on casting error
	castedOld, ok := old.(*v1.Service)
	if !ok {
		log.Errorf("Expected a Service type, got: %v", old)
		k.upToDate = false
		return
	}
	// Quick exit if resversion did not change
	if castedObj.ResourceVersion == castedOld.ResourceVersion {
		return
	}
	// Compare annotations
	if valuesDiffer(castedObj.Annotations, castedOld.Annotations, kubeServiceAnnotationPrefix) {
		log.Trace("Invalidating configs on service change")
		k.upToDate = false
		return
	}
}

func parseServiceAnnotations(services []*v1.Service) ([]integration.Config, error) {
	var configs []integration.Config
	for _, svc := range services {
		if svc == nil || svc.ObjectMeta.UID == "" {
			log.Debug("Ignoring a nil service")
			continue
		}
		service_id := apiserver.EntityForService(svc)
		c, errors := extractTemplatesFromMap(service_id, svc.Annotations, kubeServiceAnnotationPrefix)
		for _, err := range errors {
			log.Errorf("Cannot parse template for service %s/%s: %s", svc.Namespace, svc.Name, err)
		}
		// All configurations are cluster checks
		for i := range c {
			c[i].ClusterCheck = true
		}
		configs = append(configs, c...)
	}

	return configs, nil
}

// NewKubeEndpointConfigProvider returns a new ConfigProvider connected to kubelet.
// Connectivity is not checked at this stage to allow for retries, Collect will do it.
func NewKubeEndpointConfigProvider(config config.ConfigurationProviders) (ConfigProvider, error) {
	ac, err := apiserver.GetAPIClient()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to apiserver: %s", err)
	}
	endpointsInformer := ac.InformerFactory.Core().V1().Endpoints()
	if endpointsInformer == nil {
		return nil, fmt.Errorf("cannot get endpoint informer: %s", err)
	}

	p := &KubeEndpointConfigProvider{
		lister: endpointsInformer.Lister(),
	}

	endpointsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.invalidate,
		UpdateFunc: p.invalidateIfChanged,
		DeleteFunc: p.invalidate,
	})

	return p, nil
}

// String returns a string representation of the KubeEndpointConfigProvider
func (k *KubeEndpointConfigProvider) String() string {
	return KubeServices
}

// Collect retrieves services from the apiserver, builds Config objects and returns them
func (k *KubeEndpointConfigProvider) Collect() ([]integration.Config, error) {
	endpoints, err := k.lister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	k.upToDate = true

	return parseEndpointAnnotations(endpoints)
}

// IsUpToDate allows to cache configs as long as no changes are detected in the apiserver
func (k *KubeEndpointConfigProvider) IsUpToDate() (bool, error) {
	return k.upToDate, nil
}

func (k *KubeEndpointConfigProvider) invalidate(obj interface{}) {
	if obj != nil {
		log.Trace("Invalidating configs on new/deleted endpoint")
		k.upToDate = false
	}
}

func (k *KubeEndpointConfigProvider) invalidateIfChanged(old, obj interface{}) {
	// Cast the updated object, don't invalidate on casting error.
	// nil pointers are safely handled by the casting logic.
	castedObj, ok := obj.(*v1.Endpoints)
	if !ok {
		log.Errorf("Expected a Endpoints type, got: %v", obj)
		return
	}
	// Cast the old object, invalidate on casting error
	castedOld, ok := old.(*v1.Endpoints)
	if !ok {
		log.Errorf("Expected a Endpoints type, got: %v", old)
		k.upToDate = false
		return
	}
	// Quick exit if resversion did not change
	if castedObj.ResourceVersion == castedOld.ResourceVersion {
		return
	}
	// Compare annotations
	if valuesDiffer(castedObj.Annotations, castedOld.Annotations, kubeEndpointAnnotationPrefix) {
		log.Trace("Invalidating configs on endpoint change")
		k.upToDate = false
		return
	}
}

func parseEndpointAnnotations(endpoints []*v1.Endpoints) ([]integration.Config, error) {
	var configs []integration.Config
	for _, endpt := range endpoints {
		if endpt == nil || endpt.ObjectMeta.UID == "" {
			log.Debug("Ignoring a nil endpoint")
			continue
		}
		endpoint_id := apiserver.EntityForEndpoints(endpt)
		c, errors := extractTemplatesFromMap(endpoint_id, endpt.Annotations, kubeEndpointAnnotationPrefix)
		for _, err := range errors {
			log.Errorf("Cannot parse template for endpoint %s/%s: %s", endpt.Namespace, endpt.Name, err)
		}
		// All configurations are cluster checks
		for i := range c {
			c[i].ClusterCheck = true
		}
		configs = append(configs, c...)
	}

	return configs, nil
}

// valuesDiffer returns true if the annotations matching the
// given prefix are different between map first and second.
// It also counts the annotation count to catch deletions.
func valuesDiffer(first, second map[string]string, prefix string) bool {
	var matchingInFirst int
	for name, value := range first {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if second[name] != value {
			return true
		}
		matchingInFirst++
	}

	var matchingInSecond int
	for name := range second {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		matchingInSecond++
	}

	return matchingInFirst != matchingInSecond
}

func init() {
	RegisterProvider("kube_services", NewKubeServiceConfigProvider)
}
