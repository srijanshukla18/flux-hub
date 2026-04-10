package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type DiagnosticViewModel struct {
	Summary string
	Objects []DiagnosticObjectViewModel
	Events  []DiagnosticEventViewModel
}

type DiagnosticObjectViewModel struct {
	Kind   string
	Name   string
	Status string
	Detail string
}

type DiagnosticEventViewModel struct {
	Resource string
	Reason   string
	Message  string
	Age      string
}

func (a *App) helmReleaseDiagnosticVM(namespace, name string) *DiagnosticViewModel {
	cfg, err := loadKubeConfig()
	if err != nil {
		return &DiagnosticViewModel{Summary: "diagnostic scan failed: " + err.Error()}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return &DiagnosticViewModel{Summary: "diagnostic scan failed: " + err.Error()}
	}

	selector := metav1.ListOptions{LabelSelector: "app.kubernetes.io/instance=" + name}
	vm := &DiagnosticViewModel{}
	resourceKeys := map[string]bool{"HelmRelease/" + namespace + "/" + name: true}
	var errs []string

	deps, err := client.AppsV1().Deployments(namespace).List(context.Background(), selector)
	if err != nil {
		errs = append(errs, "deployments: "+err.Error())
	}
	for _, dep := range deps.Items {
		resourceKeys["Deployment/"+namespace+"/"+dep.Name] = true
		vm.Objects = append(vm.Objects, deploymentDiagnostic(dep))
	}

	stss, err := client.AppsV1().StatefulSets(namespace).List(context.Background(), selector)
	if err != nil {
		errs = append(errs, "statefulsets: "+err.Error())
	}
	for _, sts := range stss.Items {
		resourceKeys["StatefulSet/"+namespace+"/"+sts.Name] = true
		vm.Objects = append(vm.Objects, statefulSetDiagnostic(sts))
	}

	jobs, err := client.BatchV1().Jobs(namespace).List(context.Background(), selector)
	if err != nil {
		errs = append(errs, "jobs: "+err.Error())
	}
	for _, job := range jobs.Items {
		resourceKeys["Job/"+namespace+"/"+job.Name] = true
		vm.Objects = append(vm.Objects, jobDiagnostic(job))
	}

	pods, err := client.CoreV1().Pods(namespace).List(context.Background(), selector)
	if err != nil {
		errs = append(errs, "pods: "+err.Error())
	}
	for _, pod := range pods.Items {
		resourceKeys["Pod/"+namespace+"/"+pod.Name] = true
		vm.Objects = append(vm.Objects, podDiagnostic(pod))
	}

	sort.Slice(vm.Objects, func(i, j int) bool {
		if vm.Objects[i].Kind == vm.Objects[j].Kind {
			return vm.Objects[i].Name < vm.Objects[j].Name
		}
		return vm.Objects[i].Kind < vm.Objects[j].Kind
	})

	vm.Summary = diagnosticSummary(vm.Objects)

	events, err := client.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		errs = append(errs, "events: "+err.Error())
	}
	rawEvents := make([]corev1.Event, 0, len(events.Items))
	for _, evt := range events.Items {
		key := evt.InvolvedObject.Kind + "/" + namespace + "/" + evt.InvolvedObject.Name
		if !resourceKeys[key] {
			continue
		}
		if !isDiagnosticEvent(evt) {
			continue
		}
		rawEvents = append(rawEvents, evt)
	}
	sort.Slice(rawEvents, func(i, j int) bool {
		return eventTime(rawEvents[i]).After(eventTime(rawEvents[j]))
	})
	if len(rawEvents) > 6 {
		rawEvents = rawEvents[:6]
	}
	filtered := make([]DiagnosticEventViewModel, 0, len(rawEvents))
	for _, evt := range rawEvents {
		filtered = append(filtered, DiagnosticEventViewModel{
			Resource: evt.InvolvedObject.Kind + "/" + evt.InvolvedObject.Name,
			Reason:   evt.Reason,
			Message:  truncate(singleLine(evt.Message), 240),
			Age:      relativeTime(eventTime(evt)),
		})
	}
	vm.Events = filtered

	if vm.Summary == "" {
		vm.Summary = "No obvious workload cause found yet."
	}
	if len(errs) > 0 && len(vm.Objects) == 0 {
		vm.Summary = "diagnostic scan incomplete: " + truncate(strings.Join(errs, "; "), 220)
	}
	return vm
}

func deploymentDiagnostic(dep appsv1.Deployment) DiagnosticObjectViewModel {
	ready := dep.Status.ReadyReplicas
	replicas := dep.Status.Replicas
	status := fmt.Sprintf("%d/%d ready", ready, replicas)
	detail := ""
	for _, c := range dep.Status.Conditions {
		if c.Status == corev1.ConditionFalse || c.Status == corev1.ConditionUnknown {
			detail = firstNonEmpty(c.Reason, c.Message)
			break
		}
	}
	return DiagnosticObjectViewModel{Kind: "Deployment", Name: dep.Name, Status: status, Detail: detail}
}

func statefulSetDiagnostic(sts appsv1.StatefulSet) DiagnosticObjectViewModel {
	status := fmt.Sprintf("%d/%d ready", sts.Status.ReadyReplicas, sts.Status.Replicas)
	detail := ""
	if sts.Status.CurrentRevision != "" && sts.Status.UpdateRevision != "" && sts.Status.CurrentRevision != sts.Status.UpdateRevision {
		detail = "rolling update in progress"
	}
	if sts.Status.ReadyReplicas < sts.Status.Replicas && detail == "" {
		detail = "pods not ready"
	}
	return DiagnosticObjectViewModel{Kind: "StatefulSet", Name: sts.Name, Status: status, Detail: detail}
}

func jobDiagnostic(job batchv1.Job) DiagnosticObjectViewModel {
	status := fmt.Sprintf("%d succeeded / %d failed", job.Status.Succeeded, job.Status.Failed)
	detail := ""
	for _, c := range job.Status.Conditions {
		if c.Status == corev1.ConditionTrue {
			detail = firstNonEmpty(c.Reason, c.Message)
			break
		}
	}
	return DiagnosticObjectViewModel{Kind: "Job", Name: job.Name, Status: status, Detail: detail}
}

func podDiagnostic(pod corev1.Pod) DiagnosticObjectViewModel {
	status, detail := podStatusSummary(pod)
	return DiagnosticObjectViewModel{Kind: "Pod", Name: pod.Name, Status: status, Detail: detail}
}

func podStatusSummary(pod corev1.Pod) (string, string) {
	for _, s := range pod.Status.InitContainerStatuses {
		if s.State.Waiting != nil {
			return s.State.Waiting.Reason, firstNonEmpty(s.State.Waiting.Message, containerImageLabel(s.Image))
		}
		if s.State.Terminated != nil && s.State.Terminated.ExitCode != 0 {
			return s.State.Terminated.Reason, firstNonEmpty(s.State.Terminated.Message, containerImageLabel(s.Image))
		}
	}
	for _, s := range pod.Status.ContainerStatuses {
		if s.State.Waiting != nil {
			return s.State.Waiting.Reason, firstNonEmpty(s.State.Waiting.Message, containerImageLabel(s.Image))
		}
		if s.State.Terminated != nil && s.State.Terminated.ExitCode != 0 {
			return s.State.Terminated.Reason, firstNonEmpty(s.State.Terminated.Message, containerImageLabel(s.Image))
		}
	}
	ready := 0
	for _, s := range pod.Status.ContainerStatuses {
		if s.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%s (%d/%d ready)", pod.Status.Phase, ready, len(pod.Status.ContainerStatuses)), ""
}

func containerImageLabel(image string) string {
	if image == "" {
		return ""
	}
	return "image " + image
}

func diagnosticSummary(objects []DiagnosticObjectViewModel) string {
	for _, obj := range objects {
		if obj.Kind == "Pod" && obj.Status != "Running (1/1 ready)" && obj.Status != "Succeeded (1/1 ready)" {
			return fmt.Sprintf("%s %s is %s", obj.Kind, obj.Name, obj.Status)
		}
	}
	for _, obj := range objects {
		if strings.Contains(obj.Status, "0/") || strings.Contains(strings.ToLower(obj.Detail), "not ready") {
			return fmt.Sprintf("%s %s is not ready", obj.Kind, obj.Name)
		}
	}
	if len(objects) == 0 {
		return "No matching workload objects found."
	}
	return "Checked live workload objects for this release."
}

func isDiagnosticEvent(evt corev1.Event) bool {
	if strings.EqualFold(evt.Type, "Warning") {
		return true
	}
	r := strings.ToLower(evt.Reason + " " + evt.Message)
	for _, needle := range []string{"failed", "error", "backoff", "unhealthy", "timeout"} {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}

func eventTime(evt corev1.Event) time.Time {
	if !evt.LastTimestamp.IsZero() {
		return evt.LastTimestamp.Time
	}
	if evt.EventTime.Time.IsZero() {
		return evt.CreationTimestamp.Time
	}
	return evt.EventTime.Time
}
