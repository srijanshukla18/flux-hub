package app

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
	ctx := context.Background()
	var errs []string

	deps, err := client.AppsV1().Deployments(namespace).List(ctx, selector)
	if err != nil {
		errs = append(errs, "deployments: "+err.Error())
	}
	stss, err := client.AppsV1().StatefulSets(namespace).List(ctx, selector)
	if err != nil {
		errs = append(errs, "statefulsets: "+err.Error())
	}
	jobs, err := client.BatchV1().Jobs(namespace).List(ctx, selector)
	if err != nil {
		errs = append(errs, "jobs: "+err.Error())
	}
	pods, err := client.CoreV1().Pods(namespace).List(ctx, selector)
	if err != nil {
		errs = append(errs, "pods: "+err.Error())
	}
	events, err := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		errs = append(errs, "events: "+err.Error())
	}

	exactKeys := map[string]bool{"HelmRelease/" + namespace + "/" + name: true}
	prefixes := []string{name}
	for _, dep := range deps.Items {
		exactKeys["Deployment/"+namespace+"/"+dep.Name] = true
		prefixes = append(prefixes, dep.Name)
	}
	for _, sts := range stss.Items {
		exactKeys["StatefulSet/"+namespace+"/"+sts.Name] = true
		prefixes = append(prefixes, sts.Name)
	}
	for _, job := range jobs.Items {
		exactKeys["Job/"+namespace+"/"+job.Name] = true
		prefixes = append(prefixes, job.Name)
	}
	for _, pod := range pods.Items {
		exactKeys["Pod/"+namespace+"/"+pod.Name] = true
		prefixes = append(prefixes, pod.Name)
	}

	rawEvents := matchingDiagnosticEvents(events.Items, namespace, exactKeys, prefixes)

	if job, ok := firstFailedJob(jobs.Items); ok {
		objects := []DiagnosticObjectViewModel{jobDiagnostic(job)}
		badPods := badPodsForPrefix(pods.Items, job.Name)
		for _, pod := range badPods {
			objects = append(objects, podDiagnostic(pod))
			if len(objects) >= 3 {
				break
			}
		}
		return finalizeDiagnostics(DiagnosticViewModel{
			Summary: jobFailureSummary(job),
			Objects: objects,
			Events:  diagnosticEventVMs(filterEventsByPrefix(rawEvents, job.Name), 4),
		}, errs)
	}

	if dep, ok := firstProblemDeployment(deps.Items); ok {
		objects := []DiagnosticObjectViewModel{deploymentDiagnostic(dep)}
		badPods := badPodsForPrefix(pods.Items, dep.Name)
		for _, pod := range badPods {
			objects = append(objects, podDiagnostic(pod))
			if len(objects) >= 4 {
				break
			}
		}
		evts := filterEventsByPrefix(rawEvents, dep.Name)
		return finalizeDiagnostics(DiagnosticViewModel{
			Summary: deploymentFailureSummary(dep, evts),
			Objects: objects,
			Events:  diagnosticEventVMs(evts, 4),
		}, errs)
	}

	if sts, ok := firstProblemStatefulSet(stss.Items); ok {
		objects := []DiagnosticObjectViewModel{statefulSetDiagnostic(sts)}
		badPods := badPodsForPrefix(pods.Items, sts.Name)
		for _, pod := range badPods {
			objects = append(objects, podDiagnostic(pod))
			if len(objects) >= 4 {
				break
			}
		}
		evts := filterEventsByPrefix(rawEvents, sts.Name)
		return finalizeDiagnostics(DiagnosticViewModel{
			Summary: statefulSetFailureSummary(sts, evts),
			Objects: objects,
			Events:  diagnosticEventVMs(evts, 4),
		}, errs)
	}

	if pod, ok := firstBadPod(pods.Items); ok {
		summary, _ := podStatusSummary(pod)
		return finalizeDiagnostics(DiagnosticViewModel{
			Summary: fmt.Sprintf("pod %s is %s", pod.Name, summary),
			Objects: []DiagnosticObjectViewModel{podDiagnostic(pod)},
			Events:  diagnosticEventVMs(filterEventsByPrefix(rawEvents, pod.Name), 4),
		}, errs)
	}

	if len(rawEvents) > 0 {
		evt := rawEvents[0]
		return finalizeDiagnostics(DiagnosticViewModel{
			Summary: fmt.Sprintf("recent cluster event: %s on %s/%s", evt.Reason, evt.InvolvedObject.Kind, evt.InvolvedObject.Name),
			Events:  diagnosticEventVMs(rawEvents, 4),
		}, errs)
	}

	if len(deps.Items) > 0 || len(stss.Items) > 0 || len(pods.Items) > 0 {
		objects := make([]DiagnosticObjectViewModel, 0, 2)
		if len(deps.Items) > 0 {
			objects = append(objects, deploymentDiagnostic(deps.Items[0]))
		} else if len(stss.Items) > 0 {
			objects = append(objects, statefulSetDiagnostic(stss.Items[0]))
		}
		return finalizeDiagnostics(DiagnosticViewModel{
			Summary: "Current workload looks healthy. Previous release may still be serving.",
			Objects: objects,
		}, errs)
	}

	return finalizeDiagnostics(DiagnosticViewModel{Summary: "No live workload objects remain for this release."}, errs)
}

func finalizeDiagnostics(vm DiagnosticViewModel, errs []string) *DiagnosticViewModel {
	if vm.Summary == "" {
		vm.Summary = "No obvious workload cause found yet."
	}
	if len(errs) > 0 && len(vm.Objects) == 0 && len(vm.Events) == 0 {
		vm.Summary = "diagnostic scan incomplete: " + truncate(strings.Join(errs, "; "), 220)
	}
	return &vm
}

func matchingDiagnosticEvents(events []corev1.Event, namespace string, exactKeys map[string]bool, prefixes []string) []corev1.Event {
	out := make([]corev1.Event, 0)
	for _, evt := range events {
		if !isDiagnosticEvent(evt) {
			continue
		}
		key := evt.InvolvedObject.Kind + "/" + namespace + "/" + evt.InvolvedObject.Name
		if exactKeys[key] || eventMatchesPrefix(evt, prefixes) {
			out = append(out, evt)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return eventTime(out[i]).After(eventTime(out[j]))
	})
	return out
}

func eventMatchesPrefix(evt corev1.Event, prefixes []string) bool {
	name := evt.InvolvedObject.Name
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		if name == prefix || strings.HasPrefix(name, prefix+"-") {
			return true
		}
	}
	return false
}

func filterEventsByPrefix(events []corev1.Event, prefix string) []corev1.Event {
	if prefix == "" {
		return events
	}
	out := make([]corev1.Event, 0)
	for _, evt := range events {
		if evt.InvolvedObject.Name == prefix || strings.HasPrefix(evt.InvolvedObject.Name, prefix+"-") {
			out = append(out, evt)
		}
	}
	return out
}

func diagnosticEventVMs(events []corev1.Event, limit int) []DiagnosticEventViewModel {
	if limit <= 0 {
		limit = 4
	}
	if len(events) > limit {
		events = events[:limit]
	}
	out := make([]DiagnosticEventViewModel, 0, len(events))
	for _, evt := range events {
		out = append(out, DiagnosticEventViewModel{
			Resource: evt.InvolvedObject.Kind + "/" + evt.InvolvedObject.Name,
			Reason:   evt.Reason,
			Message:  truncate(singleLine(evt.Message), 240),
			Age:      relativeTime(eventTime(evt)),
		})
	}
	return out
}

func firstFailedJob(jobs []batchv1.Job) (batchv1.Job, bool) {
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Name < jobs[j].Name })
	for _, job := range jobs {
		if jobFailureDetail(job) != "" || job.Status.Failed > 0 {
			return job, true
		}
	}
	return batchv1.Job{}, false
}

func firstProblemDeployment(deps []appsv1.Deployment) (appsv1.Deployment, bool) {
	sort.Slice(deps, func(i, j int) bool { return deps[i].Name < deps[j].Name })
	for _, dep := range deps {
		if deploymentHasProblem(dep) {
			return dep, true
		}
	}
	return appsv1.Deployment{}, false
}

func firstProblemStatefulSet(stss []appsv1.StatefulSet) (appsv1.StatefulSet, bool) {
	sort.Slice(stss, func(i, j int) bool { return stss[i].Name < stss[j].Name })
	for _, sts := range stss {
		if statefulSetHasProblem(sts) {
			return sts, true
		}
	}
	return appsv1.StatefulSet{}, false
}

func firstBadPod(pods []corev1.Pod) (corev1.Pod, bool) {
	bad := badPodsForPrefix(pods, "")
	if len(bad) == 0 {
		return corev1.Pod{}, false
	}
	return bad[0], true
}

func badPodsForPrefix(pods []corev1.Pod, prefix string) []corev1.Pod {
	out := make([]corev1.Pod, 0)
	for _, pod := range pods {
		if prefix != "" && pod.Name != prefix && !strings.HasPrefix(pod.Name, prefix+"-") {
			continue
		}
		if podHasProblem(pod) {
			out = append(out, pod)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func deploymentHasProblem(dep appsv1.Deployment) bool {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	if dep.Status.AvailableReplicas < desired || dep.Status.ReadyReplicas < desired || dep.Status.UpdatedReplicas < desired {
		return true
	}
	for _, c := range dep.Status.Conditions {
		if c.Status == corev1.ConditionFalse || c.Status == corev1.ConditionUnknown {
			return true
		}
	}
	return false
}

func statefulSetHasProblem(sts appsv1.StatefulSet) bool {
	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	if sts.Status.ReadyReplicas < desired {
		return true
	}
	if sts.Status.CurrentRevision != "" && sts.Status.UpdateRevision != "" && sts.Status.CurrentRevision != sts.Status.UpdateRevision {
		return true
	}
	return false
}

func podHasProblem(pod corev1.Pod) bool {
	for _, s := range pod.Status.InitContainerStatuses {
		if s.State.Waiting != nil || (s.State.Terminated != nil && s.State.Terminated.ExitCode != 0) {
			return true
		}
	}
	for _, s := range pod.Status.ContainerStatuses {
		if s.State.Waiting != nil || (s.State.Terminated != nil && s.State.Terminated.ExitCode != 0) || !s.Ready {
			return true
		}
	}
	if pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodFailed {
		return true
	}
	return false
}

func deploymentDiagnostic(dep appsv1.Deployment) DiagnosticObjectViewModel {
	desired := dep.Status.Replicas
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	status := fmt.Sprintf("%d/%d available", dep.Status.AvailableReplicas, desired)
	detail := ""
	for _, c := range dep.Status.Conditions {
		if c.Status == corev1.ConditionFalse || c.Status == corev1.ConditionUnknown {
			detail = firstNonEmpty(c.Reason, c.Message)
			break
		}
	}
	if detail == "" && dep.Status.ReadyReplicas < desired {
		detail = fmt.Sprintf("%d/%d ready", dep.Status.ReadyReplicas, desired)
	}
	return DiagnosticObjectViewModel{Kind: "Deployment", Name: dep.Name, Status: status, Detail: detail}
}

func statefulSetDiagnostic(sts appsv1.StatefulSet) DiagnosticObjectViewModel {
	desired := sts.Status.Replicas
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	status := fmt.Sprintf("%d/%d ready", sts.Status.ReadyReplicas, desired)
	detail := ""
	if sts.Status.CurrentRevision != "" && sts.Status.UpdateRevision != "" && sts.Status.CurrentRevision != sts.Status.UpdateRevision {
		detail = "rolling update in progress"
	}
	if sts.Status.ReadyReplicas < desired && detail == "" {
		detail = "pods not ready"
	}
	return DiagnosticObjectViewModel{Kind: "StatefulSet", Name: sts.Name, Status: status, Detail: detail}
}

func jobDiagnostic(job batchv1.Job) DiagnosticObjectViewModel {
	status := fmt.Sprintf("%d succeeded / %d failed", job.Status.Succeeded, job.Status.Failed)
	detail := jobFailureDetail(job)
	if detail == "" {
		for _, c := range job.Status.Conditions {
			if c.Status == corev1.ConditionTrue {
				detail = firstNonEmpty(c.Reason, c.Message)
				break
			}
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

func jobFailureSummary(job batchv1.Job) string {
	kind := "job"
	if hook := jobHookPhase(job); hook != "" {
		kind = hook + " hook job"
	}
	detail := truncate(singleLine(jobFailureDetail(job)), 180)
	if detail == "" {
		return fmt.Sprintf("%s %s failed", kind, job.Name)
	}
	return fmt.Sprintf("%s %s failed: %s", kind, job.Name, detail)
}

func jobHookPhase(job batchv1.Job) string {
	raw := strings.TrimSpace(job.Annotations["helm.sh/hook"])
	switch {
	case strings.Contains(raw, "pre-upgrade"):
		return "pre-upgrade"
	case strings.Contains(raw, "post-upgrade"):
		return "post-upgrade"
	case strings.Contains(raw, "pre-install"):
		return "pre-install"
	case strings.Contains(raw, "post-install"):
		return "post-install"
	case raw != "":
		return raw
	default:
		return ""
	}
}

func jobFailureDetail(job batchv1.Job) string {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return firstNonEmpty(c.Reason, c.Message)
		}
	}
	for _, c := range job.Status.Conditions {
		if c.Status == corev1.ConditionTrue {
			return firstNonEmpty(c.Reason, c.Message)
		}
	}
	return ""
}

func deploymentFailureSummary(dep appsv1.Deployment, events []corev1.Event) string {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	summary := fmt.Sprintf("deployment %s is %d/%d available", dep.Name, dep.Status.AvailableReplicas, desired)
	if len(events) > 0 {
		summary += "; recent event: " + events[0].Reason
	}
	return summary
}

func statefulSetFailureSummary(sts appsv1.StatefulSet, events []corev1.Event) string {
	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	summary := fmt.Sprintf("statefulset %s is %d/%d ready", sts.Name, sts.Status.ReadyReplicas, desired)
	if len(events) > 0 {
		summary += "; recent event: " + events[0].Reason
	}
	return summary
}

func isDiagnosticEvent(evt corev1.Event) bool {
	if strings.EqualFold(evt.Type, "Warning") {
		return true
	}
	r := strings.ToLower(evt.Reason + " " + evt.Message)
	for _, needle := range []string{"failed", "error", "backoff", "unhealthy", "timeout", "deadline exceeded", "evicted"} {
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
