package app

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

func (a *App) hydrateFocusedObjects(refs []FluxObjectRef) error {
	if len(refs) == 0 {
		return nil
	}

	cfg, err := loadKubeConfig()
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return err
	}

	targets, err := discoverFluxWatchTargets(discoveryClient)
	if err != nil {
		return err
	}
	gvrByKind := map[string]schema.GroupVersionResource{}
	for _, t := range targets {
		gvrByKind[t.Name] = t.GVR
	}

	var errs []error
	for _, ref := range refs {
		gvr, ok := gvrByKind[ref.Kind]
		if !ok {
			errs = append(errs, fmt.Errorf("%s CRD not found in cluster", ref.Kind))
			continue
		}
		ns := defaultString(ref.Namespace, "default")
		obj, err := dynamicClient.Resource(gvr).Namespace(ns).Get(context.Background(), ref.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("get %s %s/%s: %w", ref.Kind, ns, ref.Name, err))
			continue
		}
		record, err := fluxObjectRecordFromUnstructured(obj)
		if err != nil {
			errs = append(errs, fmt.Errorf("project %s %s/%s: %w", ref.Kind, ns, ref.Name, err))
			continue
		}
		if err := a.store.UpsertFluxObject(record); err != nil {
			errs = append(errs, fmt.Errorf("store %s %s/%s: %w", ref.Kind, ns, ref.Name, err))
			continue
		}
	}

	return errors.Join(errs...)
}
