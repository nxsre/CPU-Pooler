package k8sclient

import (
	"context"
	"fmt"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/kubernetes/pkg/util/node"
	"log"
	"strconv"
	"time"
)

func CreatePodWatcher(ctx context.Context, namespace, resName string) (watch.Interface, error) {
	//labelSelector := fmt.Sprintf("app.kubernetes.io/instance=%s", resName)
	//log.Printf("Creating watcher for POD with label: %s", labelSelector)

	opts := metav1.ListOptions{
		TypeMeta: metav1.TypeMeta{},
		//LabelSelector: labelSelector,
		FieldSelector: fields.OneTermEqualSelector(metav1.ObjectNameField, resName).String(),
	}
	clientset, err := createClientSet()
	if err != nil {
		return nil, err
	}
	return clientset.CoreV1().Pods(namespace).Watch(ctx, opts)
}

func waitPodDeleted(ctx context.Context, namespace, resName string) error {
	watcher, err := CreatePodWatcher(ctx, namespace, resName)
	if err != nil {
		return err
	}

	defer watcher.Stop()

	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Deleted {
				log.Printf("The POD \"%s\" is deleted", resName)
				return nil
			}

		case <-ctx.Done():
			log.Printf("Exit from waitPodDeleted for POD \"%s\" because the context is done", resName)
			return ctx.Err()
		}
	}
}

func waitPodRunning(ctx context.Context, namespace, resName string) error {
	watcher, err := CreatePodWatcher(ctx, namespace, resName)
	if err != nil {
		return err
	}

	defer watcher.Stop()

	for {
		select {
		case event := <-watcher.ResultChan():
			pod := event.Object.(*v1.Pod)

			if pod.Status.Phase == v1.PodRunning {
				log.Printf("The POD \"%s\" is running", resName)
				return nil
			}
		case <-ctx.Done():
			log.Printf("Exit from waitPodRunning for POD \"%s\" because the context is done", resName)
			return ctx.Err()
		}
	}
}

func WaitPod(ctx context.Context, namespace, podName string, annotations map[string]string) (*v1.Pod, error) {
	watcher, err := CreatePodWatcher(ctx, namespace, podName)
	if err != nil {
		return nil, err
	}
	defer watcher.Stop()
	for {
		select {
		case event := <-watcher.ResultChan():
			pod := event.Object.(*v1.Pod)
			if pod.Status.Phase == v1.PodRunning {
				log.Printf("The POD \"%s\" is running", podName)
				current := pod.ObjectMeta.GetAnnotations()
				var identity bool
				for k, v := range annotations {
					cv, ok := current[k]
					if ok && cv == v {
						identity = true
					} else {
						identity = false
						break
					}
				}
				if identity {
					log.Printf("The POD \"%s\" is running, annotations: %s", podName, annotations)
					return pod, nil
				}
				continue
			}
		case <-ctx.Done():
			log.Printf("Exit from waitPodRunning for POD \"%s\" because the context is done", podName)
			return nil, ctx.Err()
		}
	}
}

// PodStatus 用于获取准确的 pod 运行状态，pod 的 Status.Phase 只标记整个 pod 的运行状态，某个 container 异常时不会影响 pod 的 Running 状态
func PodStatus(pod *v1.Pod) (restarts int, lastRestartDate metav1.Time, restartsStr, reason string) {
	// 参考 kubectl 判断状态
	// https://github.com/kubernetes/kubernetes/blob/release-1.28/pkg/printers/internalversion/printers.go#L945-L949
	restarts = 0
	restartableInitContainerRestarts := 0
	totalContainers := len(pod.Spec.Containers)
	readyContainers := 0
	lastRestartDate = metav1.NewTime(time.Time{})
	lastRestartableInitContainerRestartDate := metav1.NewTime(time.Time{})

	reason = string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}

	// If the Pod carries {type:PodScheduled, reason:WaitingForGates}, set reason to 'SchedulingGated'.
	for _, condition := range pod.Status.Conditions {
		if condition.Type == v1.PodScheduled && condition.Reason == v1.PodReasonSchedulingGated {
			reason = v1.PodReasonSchedulingGated
		}
	}

	initContainers := make(map[string]*v1.Container)
	for i := range pod.Spec.InitContainers {
		initContainers[pod.Spec.InitContainers[i].Name] = &pod.Spec.InitContainers[i]
		if isRestartableInitContainer(&pod.Spec.InitContainers[i]) {
			totalContainers++
		}
	}

	initializing := false
	for i := range pod.Status.InitContainerStatuses {
		container := pod.Status.InitContainerStatuses[i]
		restarts += int(container.RestartCount)
		if container.LastTerminationState.Terminated != nil {
			terminatedDate := container.LastTerminationState.Terminated.FinishedAt
			if lastRestartDate.Before(&terminatedDate) {
				lastRestartDate = terminatedDate
			}
		}
		if isRestartableInitContainer(initContainers[container.Name]) {
			restartableInitContainerRestarts += int(container.RestartCount)
			if container.LastTerminationState.Terminated != nil {
				terminatedDate := container.LastTerminationState.Terminated.FinishedAt
				if lastRestartableInitContainerRestartDate.Before(&terminatedDate) {
					lastRestartableInitContainerRestartDate = terminatedDate
				}
			}
		}
		switch {
		case container.State.Terminated != nil && container.State.Terminated.ExitCode == 0:
			continue
		case isRestartableInitContainer(initContainers[container.Name]) &&
			container.Started != nil && *container.Started:
			if container.Ready {
				readyContainers++
			}
			continue
		case container.State.Terminated != nil:
			// initialization is failed
			if len(container.State.Terminated.Reason) == 0 {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Init:Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("Init:ExitCode:%d", container.State.Terminated.ExitCode)
				}
			} else {
				reason = "Init:" + container.State.Terminated.Reason
			}
			initializing = true
		case container.State.Waiting != nil && len(container.State.Waiting.Reason) > 0 && container.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + container.State.Waiting.Reason
			initializing = true
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
			initializing = true
		}
		break
	}

	if !initializing || isPodInitializedConditionTrue(&pod.Status) {
		restarts = restartableInitContainerRestarts
		lastRestartDate = lastRestartableInitContainerRestartDate
		hasRunning := false
		for i := len(pod.Status.ContainerStatuses) - 1; i >= 0; i-- {
			container := pod.Status.ContainerStatuses[i]

			restarts += int(container.RestartCount)
			if container.LastTerminationState.Terminated != nil {
				terminatedDate := container.LastTerminationState.Terminated.FinishedAt
				if lastRestartDate.Before(&terminatedDate) {
					lastRestartDate = terminatedDate
				}
			}
			if container.State.Waiting != nil && container.State.Waiting.Reason != "" {
				reason = container.State.Waiting.Reason
			} else if container.State.Terminated != nil && container.State.Terminated.Reason != "" {
				reason = container.State.Terminated.Reason
			} else if container.State.Terminated != nil && container.State.Terminated.Reason == "" {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("ExitCode:%d", container.State.Terminated.ExitCode)
				}
			} else if container.Ready && container.State.Running != nil {
				hasRunning = true
				readyContainers++
			}
		}

		// change pod status back to "Running" if there is at least one container still reporting as "Running" status
		if reason == "Completed" && hasRunning {
			if hasPodReadyCondition(pod.Status.Conditions) {
				reason = "Running"
			} else {
				reason = "NotReady"
			}
		}
	}

	if pod.DeletionTimestamp != nil && pod.Status.Reason == node.NodeUnreachablePodReason {
		reason = "Unknown"
	} else if pod.DeletionTimestamp != nil {
		reason = "Terminating"
	}

	restartsStr = strconv.Itoa(restarts)
	if restarts != 0 && !lastRestartDate.IsZero() {
		restartsStr = fmt.Sprintf("%d (%s ago)", restarts, translateTimestampSince(lastRestartDate))
	}

	//fmt.Println("重启次数", restartsStr, lastRestartDate.String(), "reason", reason, pod.Name, pod.Namespace)
	return restarts, lastRestartDate, restartsStr, reason
}

// translateTimestampSince returns the elapsed time since timestamp in
// human-readable approximation.
func translateTimestampSince(timestamp metav1.Time) string {
	if timestamp.IsZero() {
		return "<unknown>"
	}

	return duration.HumanDuration(time.Since(timestamp.Time))
}

func isRestartableInitContainer(initContainer *v1.Container) bool {
	if initContainer == nil {
		return false
	}
	if initContainer.RestartPolicy == nil {
		return false
	}

	return *initContainer.RestartPolicy == v1.ContainerRestartPolicyAlways
}

func isPodInitializedConditionTrue(status *v1.PodStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Type != v1.PodInitialized {
			continue
		}

		return condition.Status == v1.ConditionTrue
	}
	return false
}

func hasPodReadyCondition(conditions []v1.PodCondition) bool {
	for _, condition := range conditions {
		if condition.Type == v1.PodReady && condition.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}
