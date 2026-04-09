package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

type fluxWatchTarget struct {
	Name string
	GVR  schema.GroupVersionResource
}

func (a *App) runFluxWatches(ctx context.Context) error {
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

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dynamicClient, 0, metav1.NamespaceAll, nil)
	informers := make([]cache.SharedIndexInformer, 0, len(targets))
	watchedNames := make([]string, 0, len(targets))

	for _, target := range targets {
		informer := factory.ForResource(target.GVR).Informer()
		informers = append(informers, informer)
		watchedNames = append(watchedNames, target.Name)
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				a.handleWatchUpsert(obj)
			},
			UpdateFunc: func(_, newObj any) {
				a.handleWatchUpsert(newObj)
			},
			DeleteFunc: func(obj any) {
				a.handleWatchDelete(obj)
			},
		})
	}

	factory.Start(ctx.Done())
	for _, informer := range informers {
		if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			return fmt.Errorf("failed waiting for Flux informer cache sync")
		}
	}

	a.watchStatus.Set("connected", "watching "+strings.Join(watchedNames, ", "))
	log.Printf("flux watches connected: %s", strings.Join(watchedNames, ", "))
	<-ctx.Done()
	return nil
}

func (a *App) handleWatchUpsert(obj any) {
	u, err := unstructuredFromWatchObject(obj)
	if err != nil {
		log.Printf("watch upsert: %v", err)
		return
	}

	record, err := fluxObjectRecordFromUnstructured(u)
	if err != nil {
		log.Printf("watch upsert projection error for %s/%s kind=%s: %v", u.GetNamespace(), u.GetName(), u.GetKind(), err)
		return
	}
	if err := a.store.UpsertFluxObject(record); err != nil {
		log.Printf("watch upsert store error for %s/%s kind=%s: %v", record.Namespace, record.Name, record.Kind, err)
	}
}

func (a *App) handleWatchDelete(obj any) {
	u, err := unstructuredFromWatchObject(obj)
	if err != nil {
		log.Printf("watch delete: %v", err)
		return
	}
	group := u.GroupVersionKind().Group
	if group == "" {
		group = apiGroup(u.GetAPIVersion())
	}
	if err := a.store.DeleteFluxObject(group, u.GetKind(), defaultString(u.GetNamespace(), "default"), u.GetName()); err != nil {
		log.Printf("watch delete store error for %s/%s kind=%s: %v", u.GetNamespace(), u.GetName(), u.GetKind(), err)
	}
}

func unstructuredFromWatchObject(obj any) (*unstructured.Unstructured, error) {
	switch t := obj.(type) {
	case *unstructured.Unstructured:
		return t, nil
	case cache.DeletedFinalStateUnknown:
		if u, ok := t.Obj.(*unstructured.Unstructured); ok {
			return u, nil
		}
		return nil, fmt.Errorf("unexpected tombstone object: %T", t.Obj)
	default:
		return nil, fmt.Errorf("unexpected watch object type: %T", obj)
	}
}

func loadKubeConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home for kubeconfig: %w", err)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load kube config: %w", err)
	}
	return cfg, nil
}

func discoverFluxWatchTargets(client discovery.DiscoveryInterface) ([]fluxWatchTarget, error) {
	type targetSpec struct {
		group    string
		resource string
		label    string
	}
	wanted := []targetSpec{
		{group: "source.toolkit.fluxcd.io", resource: "gitrepositories", label: "GitRepository"},
		{group: "kustomize.toolkit.fluxcd.io", resource: "kustomizations", label: "Kustomization"},
		{group: "helm.toolkit.fluxcd.io", resource: "helmreleases", label: "HelmRelease"},
	}

	lists, err := client.ServerPreferredResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return nil, err
	}

	found := map[string]fluxWatchTarget{}
	for _, list := range lists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, resource := range list.APIResources {
			if strings.Contains(resource.Name, "/") {
				continue
			}
			for _, want := range wanted {
				if gv.Group == want.group && resource.Name == want.resource {
					found[want.resource] = fluxWatchTarget{
						Name: want.label,
						GVR:  schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: resource.Name},
					}
				}
			}
		}
	}

	out := make([]fluxWatchTarget, 0, len(wanted))
	missing := make([]string, 0)
	for _, want := range wanted {
		target, ok := found[want.resource]
		if !ok {
			missing = append(missing, want.label)
			continue
		}
		out = append(out, target)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no Flux CRDs discovered")
	}
	if len(missing) > 0 {
		log.Printf("flux watch discovery missing resources: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func fluxObjectRecordFromUnstructured(u *unstructured.Unstructured) (FluxObjectRecord, error) {
	group := u.GroupVersionKind().Group
	if group == "" {
		group = apiGroup(u.GetAPIVersion())
	}
	kind := u.GetKind()
	namespace := defaultString(u.GetNamespace(), "default")
	name := u.GetName()

	sourceKind, sourceNamespace, sourceName := sourceReferenceFromObject(u)
	revision := firstNonEmpty(
		nestedString(u.Object, "status", "artifact", "revision"),
		nestedString(u.Object, "status", "lastAppliedRevision"),
		nestedString(u.Object, "status", "lastAttemptedRevision"),
	)
	lastApplied := nestedString(u.Object, "status", "lastAppliedRevision")
	lastAttempted := nestedString(u.Object, "status", "lastAttemptedRevision")
	commitSHA := commitSHAFromRevision(firstNonEmpty(revision, lastApplied, lastAttempted))

	conditions := conditionsByType(u)
	ready := conditions["ready"]
	reconciling := conditions["reconciling"]
	stalled := conditions["stalled"]
	state := deriveFluxObjectState(revision, ready, reconciling, stalled)

	return FluxObjectRecord{
		APIGroup:            group,
		APIVersion:          u.GetAPIVersion(),
		Kind:                kind,
		Namespace:           namespace,
		Name:                name,
		SourceKind:          sourceKind,
		SourceNamespace:     sourceNamespace,
		SourceName:          sourceName,
		Revision:            revision,
		LastAppliedRevision: lastApplied,
		LastAttemptedRev:    lastAttempted,
		CommitSHA:           commitSHA,
		State:               state,
		Ready:               ready,
		Reconciling:         reconciling,
		Stalled:             stalled,
		ObservedGeneration:  nestedInt64(u.Object, "status", "observedGeneration"),
		Generation:          u.GetGeneration(),
		IntervalSeconds:     parseDurationSeconds(firstNonEmpty(nestedString(u.Object, "spec", "interval"), nestedString(u.Object, "spec", "chart", "spec", "interval"))),
		UpdatedAt:           time.Now().UTC(),
	}, nil
}

func sourceReferenceFromObject(u *unstructured.Unstructured) (kind, namespace, name string) {
	if ref := nestedMap(u.Object, "spec", "sourceRef"); len(ref) > 0 {
		return nestedMapString(ref, "kind"), nestedMapString(ref, "namespace"), nestedMapString(ref, "name")
	}
	if ref := nestedMap(u.Object, "spec", "chart", "spec", "sourceRef"); len(ref) > 0 {
		return nestedMapString(ref, "kind"), nestedMapString(ref, "namespace"), nestedMapString(ref, "name")
	}
	if ref := nestedMap(u.Object, "spec", "chartRef"); len(ref) > 0 {
		return nestedMapString(ref, "kind"), nestedMapString(ref, "namespace"), nestedMapString(ref, "name")
	}
	return "", "", ""
}

func deriveFluxObjectState(revision string, ready, reconciling, stalled ConditionSnapshot) string {
	if strings.EqualFold(stalled.Status, "True") {
		return "stalled"
	}
	if strings.EqualFold(reconciling.Status, "True") {
		return "reconciling"
	}
	if strings.EqualFold(ready.Status, "True") {
		return "ready"
	}
	if strings.EqualFold(ready.Status, "False") {
		return "failed"
	}
	if revision != "" {
		return "observed"
	}
	return "unknown"
}

func conditionsByType(u *unstructured.Unstructured) map[string]ConditionSnapshot {
	out := map[string]ConditionSnapshot{}
	items, _, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil {
		return out
	}
	for _, item := range items {
		condition, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typeName := strings.ToLower(strings.TrimSpace(nestedMapString(condition, "type")))
		if typeName == "" {
			continue
		}
		out[typeName] = ConditionSnapshot{
			Status:         nestedMapString(condition, "status"),
			Reason:         nestedMapString(condition, "reason"),
			Message:        nestedMapString(condition, "message"),
			LastTransition: nestedMapString(condition, "lastTransitionTime"),
		}
	}
	return out
}

func nestedString(obj map[string]any, fields ...string) string {
	value, found, err := unstructured.NestedString(obj, fields...)
	if err != nil || !found {
		return ""
	}
	return value
}

func nestedInt64(obj map[string]any, fields ...string) int64 {
	value, found, err := unstructured.NestedInt64(obj, fields...)
	if err == nil && found {
		return value
	}
	value64, found, err := unstructured.NestedFloat64(obj, fields...)
	if err == nil && found {
		return int64(value64)
	}
	return 0
}

func nestedMap(obj map[string]any, fields ...string) map[string]any {
	value, found, err := unstructured.NestedMap(obj, fields...)
	if err != nil || !found {
		return nil
	}
	return value
}

func nestedMapString(obj map[string]any, key string) string {
	value, _ := obj[key].(string)
	return value
}

func parseDurationSeconds(raw string) int64 {
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	return int64(d.Seconds())
}

func apiGroup(apiVersion string) string {
	if idx := strings.Index(apiVersion, "/"); idx >= 0 {
		return apiVersion[:idx]
	}
	return ""
}
