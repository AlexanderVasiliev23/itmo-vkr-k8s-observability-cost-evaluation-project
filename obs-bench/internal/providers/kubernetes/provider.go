package kubernetes

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type IKubernetesProvider interface {
	CreateNamespace(ctx context.Context, namespace string) error
	DeleteNamespace(ctx context.Context, namespace string) error
	RecreateNamespace(ctx context.Context, namespace string) error
	CreateMetricsExporterDeployment(ctx context.Context, namespace string, tag string, series int) error
	CreateService(ctx context.Context, namespace string) error
	CreateServiceMonitor(ctx context.Context, namespace, name, port string, matchLabels map[string]string) error
	CreateDiskMetricsService(ctx context.Context, namespace string) error
	CreateVMServiceScrape(ctx context.Context, namespace string, targetNamespace string) error
	DeleteVMServiceScrape(ctx context.Context, namespace string) error
	DeleteVMWebhooks(ctx context.Context) error
	DeleteVMClusterRoles(ctx context.Context) error
	DeleteKubePrometheusStackWebhooks(ctx context.Context) error
	CreateDiskMetricsExporter(ctx context.Context, namespace string, tag string, pvcName string) error
	WaitForLokiSingleBinaryDataPVC(ctx context.Context, namespace string, timeout time.Duration) (pvcName string, err error)
	CreateLogProducerDeployment(ctx context.Context, namespace, image, backend string, logsPerSec int, lokiPushURL, openSearchBaseURL string) error
	DeleteLogProducerDeployment(ctx context.Context, namespace string) error
	PortForwardService(ctx context.Context, namespace, serviceName string, localPort, remotePort int) (stop chan struct{}, err error)
}

type loggingTransport struct{ rt http.RoundTripper }

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	slog.DebugContext(req.Context(), "k8s request", "method", req.Method, "url", req.URL)
	resp, err := t.rt.RoundTrip(req)
	if resp != nil {
		slog.DebugContext(req.Context(), "k8s response", "status", resp.StatusCode)
	}
	return resp, err
}

type slogWriter struct{}

func (w *slogWriter) Write(p []byte) (int, error) {
	slog.DebugContext(context.Background(), strings.TrimSpace(string(p)))
	return len(p), nil
}

type provider struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	restConfig    *rest.Config
}

func NewKubernetesProvider() (IKubernetesProvider, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, err
	}
	config.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return &loggingTransport{rt: rt}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &provider{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		restConfig:    config,
	}, nil
}

func (p *provider) CreateNamespace(ctx context.Context, namespace string) error {
	ns := &corev1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: namespace,
		},
	}

	_, err := p.clientset.CoreV1().Namespaces().Create(ctx, ns, v1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (p *provider) DeleteNamespace(ctx context.Context, namespace string) error {
	ns, err := p.clientset.CoreV1().Namespaces().Get(ctx, namespace, v1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// Не дёргать Delete, если уже Terminating.
	if ns.Status.Phase != corev1.NamespaceTerminating {
		if err := p.clientset.CoreV1().Namespaces().Delete(ctx, ns.Name, v1.DeleteOptions{}); err != nil {
			return err
		}
	}

	// Ждём удаления; PVC/STS с finalizer могут зависнуть.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	forceAfter := time.Now().Add(2 * time.Minute)
	forcedOnce := false

	for {
		select {
		case <-ticker.C:
			current, err := p.clientset.CoreV1().Namespaces().Get(ctx, namespace, v1.GetOptions{})
			if k8serrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if current.Status.Phase == corev1.NamespaceTerminating && !forcedOnce && time.Now().After(forceAfter) {
				forcedOnce = true
				p.forceFinalizeNamespaceResources(ctx, namespace)
			}
		case <-ctx.Done():
			p.forceFinalizeNamespaceResources(context.Background(), namespace)
			current, err := p.clientset.CoreV1().Namespaces().Get(context.Background(), namespace, v1.GetOptions{})
			if k8serrors.IsNotFound(err) {
				return nil
			}
			if err == nil && current.Status.Phase == corev1.NamespaceTerminating {
				return fmt.Errorf("namespace %q всё ещё Terminating после снятия финализаторов: %w", namespace, ctx.Err())
			}
			if err != nil {
				return err
			}
			return ctx.Err()
		}
	}
}

// forceFinalizeNamespaceResources снимает finalizers с ресурсов и namespace, если Terminating застрял.
func (p *provider) forceFinalizeNamespaceResources(ctx context.Context, namespace string) {
	patch := []byte(`{"metadata":{"finalizers":[]}}`)

	strip := func(err error, kind string, name string) {
		if err != nil {
			slog.WarnContext(ctx, "strip finalizers", "namespace", namespace, "kind", kind, "name", name, "err", err)
		}
	}

	pvcs, err := p.clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		strip(err, "PersistentVolumeClaim", "(list)")
	} else {
		for _, pvc := range pvcs.Items {
			if len(pvc.Finalizers) == 0 {
				continue
			}
			_, err := p.clientset.CoreV1().PersistentVolumeClaims(namespace).Patch(ctx, pvc.Name, types.MergePatchType, patch, v1.PatchOptions{})
			strip(err, "PersistentVolumeClaim", pvc.Name)
		}
	}

	pods, err := p.clientset.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		strip(err, "Pod", "(list)")
	} else {
		for _, pod := range pods.Items {
			if len(pod.Finalizers) == 0 {
				continue
			}
			_, err := p.clientset.CoreV1().Pods(namespace).Patch(ctx, pod.Name, types.MergePatchType, patch, v1.PatchOptions{})
			strip(err, "Pod", pod.Name)
		}
	}

	stss, err := p.clientset.AppsV1().StatefulSets(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		strip(err, "StatefulSet", "(list)")
	} else {
		for _, sts := range stss.Items {
			if len(sts.Finalizers) == 0 {
				continue
			}
			_, err := p.clientset.AppsV1().StatefulSets(namespace).Patch(ctx, sts.Name, types.MergePatchType, patch, v1.PatchOptions{})
			strip(err, "StatefulSet", sts.Name)
		}
	}

	depls, err := p.clientset.AppsV1().Deployments(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		strip(err, "Deployment", "(list)")
	} else {
		for _, d := range depls.Items {
			if len(d.Finalizers) == 0 {
				continue
			}
			_, err := p.clientset.AppsV1().Deployments(namespace).Patch(ctx, d.Name, types.MergePatchType, patch, v1.PatchOptions{})
			strip(err, "Deployment", d.Name)
		}
	}

	ns, err := p.clientset.CoreV1().Namespaces().Get(ctx, namespace, v1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return
	}
	if err != nil {
		strip(err, "Namespace", namespace)
		return
	}
	if len(ns.Spec.Finalizers) == 0 {
		return
	}
	ns.Spec.Finalizers = nil
	if _, err := p.clientset.CoreV1().Namespaces().Finalize(ctx, ns, v1.UpdateOptions{}); err != nil {
		strip(err, "Namespace.Finalize", namespace)
	}
}

func (p *provider) RecreateNamespace(ctx context.Context, namespace string) error {
	if err := p.DeleteNamespace(ctx, namespace); err != nil {
		return err
	}

	p.deleteReleasedPVsForNamespace(ctx, namespace)

	if err := p.CreateNamespace(ctx, namespace); err != nil {
		return err
	}

	return nil
}

// deleteReleasedPVsForNamespace удаляет Released PV для namespace, чтобы provisioner очистил данные.
func (p *provider) deleteReleasedPVsForNamespace(ctx context.Context, namespace string) {
	pvList, err := p.clientset.CoreV1().PersistentVolumes().List(ctx, v1.ListOptions{})
	if err != nil {
		slog.WarnContext(ctx, "deleteReleasedPVs: list PVs", "namespace", namespace, "err", err)
		return
	}

	deleted := 0
	for _, pv := range pvList.Items {
		if pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.Namespace != namespace {
			continue
		}
		if pv.Status.Phase != corev1.VolumeReleased {
			continue
		}
		if err := p.clientset.CoreV1().PersistentVolumes().Delete(ctx, pv.Name, v1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
			slog.WarnContext(ctx, "deleteReleasedPVs: delete PV", "pv", pv.Name, "err", err)
		} else {
			deleted++
		}
	}

	if deleted > 0 {
		// Небольшая пауза, чтобы provisioner успел обработать событие и удалить директорию.
		slog.InfoContext(ctx, "deleteReleasedPVs: ждём очистки provisioner", "namespace", namespace, "deleted", deleted)
		time.Sleep(3 * time.Second)
	}
}

func (p *provider) CreateMetricsExporterDeployment(ctx context.Context, namespace string, tag string, series int) error {
	replicas := int32(1)

	deployment := &appsv1.Deployment{
		ObjectMeta: v1.ObjectMeta{
			Name:      "metrics-exporter",
			Namespace: namespace,
			Labels:    map[string]string{"app": "metrics-exporter"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &v1.LabelSelector{
				MatchLabels: map[string]string{"app": "metrics-exporter"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{"app": "metrics-exporter"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "exporter",
							Image:           tag,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{Name: "SERIES_COUNT", Value: strconv.Itoa(series)},
								{Name: "PORT", Value: "8080"},
							},
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080, Name: "metrics"},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("32Mi"),
									corev1.ResourceCPU:    resource.MustParse("10m"),
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := p.clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, v1.CreateOptions{})
	return err
}

func (p *provider) CreateLogProducerDeployment(ctx context.Context, namespace, image, backend string, logsPerSec int, lokiPushURL, openSearchBaseURL string) error {
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: v1.ObjectMeta{
			Name:      "log-producer",
			Namespace: namespace,
			Labels:    map[string]string{"app": "log-producer"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &v1.LabelSelector{
				MatchLabels: map[string]string{"app": "log-producer"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{"app": "log-producer"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "producer",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{Name: "BACKEND", Value: backend},
								{Name: "LOGS_PER_SEC", Value: strconv.Itoa(logsPerSec)},
								{Name: "LOKI_PUSH_URL", Value: lokiPushURL},
								{Name: "OPENSEARCH_BASE_URL", Value: openSearchBaseURL},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("64Mi"),
									corev1.ResourceCPU:    resource.MustParse("50m"),
								},
							},
						},
					},
				},
			},
		},
	}
	_, err := p.clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, v1.CreateOptions{})
	return err
}

func (p *provider) DeleteLogProducerDeployment(ctx context.Context, namespace string) error {
	err := p.clientset.AppsV1().Deployments(namespace).Delete(ctx, "log-producer", v1.DeleteOptions{})
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (p *provider) CreateService(ctx context.Context, namespace string) error {
	svc := &corev1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:      "metrics-exporter",
			Namespace: namespace,
			Labels:    map[string]string{"app": "metrics-exporter"},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "metrics-exporter"},
			Ports: []corev1.ServicePort{
				{
					Name:       "metrics",
					Port:       8080,
					TargetPort: intstr.FromInt32(8080),
				},
			},
		},
	}

	_, err := p.clientset.CoreV1().Services(namespace).Create(ctx, svc, v1.CreateOptions{})

	return err
}

// Интервал для целей, которые видит kube-prometheus-stack (низкая кардинальность: disk / self-metrics).
const serviceMonitorScrapeInterval = "30s"

func (p *provider) CreateServiceMonitor(ctx context.Context, namespace, name, port string, matchLabels map[string]string) error {
	gvr := schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "servicemonitors",
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"release": "kube-prometheus-stack",
				},
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"matchLabels": func() map[string]interface{} {
						m := make(map[string]interface{}, len(matchLabels))
						for k, v := range matchLabels {
							m[k] = v
						}
						return m
					}(),
				},
				"endpoints": []interface{}{
					map[string]interface{}{
						"port":          port,
						"path":          "/metrics",
						"interval":      serviceMonitorScrapeInterval,
						"scrapeTimeout": "20s",
					},
				},
			},
		},
	}

	if _, err := p.dynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, obj, v1.CreateOptions{}); err != nil {
		return err
	}

	return nil
}

func (p *provider) CreateDiskMetricsService(ctx context.Context, namespace string) error {
	svc := &corev1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:      "disk-metrics-exporter",
			Namespace: namespace,
			Labels:    map[string]string{"app": "disk-metrics-exporter"},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "disk-metrics-exporter"},
			Ports: []corev1.ServicePort{
				{
					Name:       "metrics",
					Port:       8080,
					TargetPort: intstr.FromInt32(8080),
				},
			},
		},
	}

	_, err := p.clientset.CoreV1().Services(namespace).Create(ctx, svc, v1.CreateOptions{})
	return err
}

func (p *provider) CreateVMServiceScrape(ctx context.Context, namespace string, targetNamespace string) error {
	gvr := schema.GroupVersionResource{
		Group:    "operator.victoriametrics.com",
		Version:  "v1beta1",
		Resource: "vmservicescrapes",
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operator.victoriametrics.com/v1beta1",
			"kind":       "VMServiceScrape",
			"metadata": map[string]interface{}{
				"name":      "metrics-exporter",
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"namespaceSelector": map[string]interface{}{
					"matchNames": []interface{}{targetNamespace},
				},
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": "metrics-exporter",
					},
				},
				"endpoints": []interface{}{
					map[string]interface{}{
						"port":          "metrics",
						"path":          "/metrics",
						"interval":      serviceMonitorScrapeInterval,
						"scrapeTimeout": "20s",
					},
				},
			},
		},
	}

	_, err := p.dynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, obj, v1.CreateOptions{})
	return err
}

func (p *provider) DeleteVMServiceScrape(ctx context.Context, namespace string) error {
	gvrs := []schema.GroupVersionResource{
		{Group: "operator.victoriametrics.com", Version: "v1beta1", Resource: "vmservicescrapes"},
		{Group: "operator.victoriametrics.com", Version: "v1beta1", Resource: "vmagents"},
		{Group: "operator.victoriametrics.com", Version: "v1beta1", Resource: "vmalertmanagers"},
		{Group: "operator.victoriametrics.com", Version: "v1beta1", Resource: "vmalerts"},
		{Group: "operator.victoriametrics.com", Version: "v1beta1", Resource: "vmsingles"},
	}

	patch := []byte(`{"metadata":{"finalizers":[]}}`)

	for _, gvr := range gvrs {
		list, err := p.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, v1.ListOptions{})
		if err != nil {
			continue
		}
		for _, item := range list.Items {
			name := item.GetName()
			_, err := p.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, v1.PatchOptions{})
			if err != nil && !k8serrors.IsNotFound(err) {
				return err
			}
			err = p.dynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, name, v1.DeleteOptions{})
			if err != nil && !k8serrors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

func (p *provider) DeleteKubePrometheusStackWebhooks(ctx context.Context) error {
	for _, resource := range []string{"validatingwebhookconfigurations", "mutatingwebhookconfigurations"} {
		gvr := schema.GroupVersionResource{
			Group:    "admissionregistration.k8s.io",
			Version:  "v1",
			Resource: resource,
		}
		list, err := p.dynamicClient.Resource(gvr).List(ctx, v1.ListOptions{
			LabelSelector: "app=kube-prometheus-stack-admission",
		})
		if err != nil {
			return err
		}
		for _, item := range list.Items {
			if err := p.dynamicClient.Resource(gvr).Delete(ctx, item.GetName(), v1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func (p *provider) DeleteVMWebhooks(ctx context.Context) error {
	gvr := schema.GroupVersionResource{
		Group:    "admissionregistration.k8s.io",
		Version:  "v1",
		Resource: "validatingwebhookconfigurations",
	}

	list, err := p.dynamicClient.Resource(gvr).List(ctx, v1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=victoria-metrics-k8s-stack",
	})
	if err != nil {
		return err
	}

	for _, item := range list.Items {
		err := p.dynamicClient.Resource(gvr).Delete(ctx, item.GetName(), v1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func (p *provider) DeleteVMClusterRoles(ctx context.Context) error {
	const releaseName = "victoria-metrics-k8s-stack"

	isVMResource := func(item unstructured.Unstructured) bool {
		return item.GetAnnotations()["meta.helm.sh/release-name"] == releaseName
	}

	deleteMatching := func(gvr schema.GroupVersionResource, namespace string) error {
		var list *unstructured.UnstructuredList
		var err error
		if namespace == "" {
			list, err = p.dynamicClient.Resource(gvr).List(ctx, v1.ListOptions{})
		} else {
			list, err = p.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, v1.ListOptions{})
		}
		if err != nil {
			return err
		}
		for _, item := range list.Items {
			if !isVMResource(item) {
				continue
			}
			var delErr error
			if namespace == "" {
				delErr = p.dynamicClient.Resource(gvr).Delete(ctx, item.GetName(), v1.DeleteOptions{})
			} else {
				delErr = p.dynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, item.GetName(), v1.DeleteOptions{})
			}
			if delErr != nil && !k8serrors.IsNotFound(delErr) {
				return delErr
			}
		}
		return nil
	}

	// Cluster-scoped RBAC.
	for _, resource := range []string{"clusterroles", "clusterrolebindings"} {
		gvr := schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: resource}
		if err := deleteMatching(gvr, ""); err != nil {
			return err
		}
	}

	// Service-ресурсы в kube-system.
	if err := deleteMatching(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, "kube-system"); err != nil {
		return err
	}

	return nil
}

// pvcNodeName находит ноду, на которой смонтирован PVC (RWO → один узел).
func (p *provider) pvcNodeName(ctx context.Context, namespace, pvcName string) (string, error) {
	pods, err := p.clientset.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning || pod.Spec.NodeName == "" {
			continue
		}
		for _, vol := range pod.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ClaimName == pvcName {
				return pod.Spec.NodeName, nil
			}
		}
	}
	return "", nil
}

func (p *provider) CreateDiskMetricsExporter(ctx context.Context, namespace string, tag string, pvcName string) error {
	replicas := int32(1)

	nodeName, err := p.pvcNodeName(ctx, namespace, pvcName)
	if err != nil {
		slog.WarnContext(ctx, "could not resolve PVC node, skipping nodeSelector", "pvc", pvcName, "err", err)
	}

	var nodeSelector map[string]string
	if nodeName != "" {
		slog.InfoContext(ctx, "disk-metrics-exporter pinned to node", "node", nodeName, "pvc", pvcName)
		nodeSelector = map[string]string{"kubernetes.io/hostname": nodeName}
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: v1.ObjectMeta{
			Name:      "disk-metrics-exporter",
			Namespace: namespace,
			Labels:    map[string]string{"app": "disk-metrics-exporter"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &v1.LabelSelector{
				MatchLabels: map[string]string{"app": "disk-metrics-exporter"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{"app": "disk-metrics-exporter"},
				},
				Spec: corev1.PodSpec{
					NodeSelector: nodeSelector,
					Volumes: []corev1.Volume{
						{
							Name: "vm-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: pvcName,
									ReadOnly:  true,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "exporter",
							Image:           tag,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{Name: "MOUNT_PATH", Value: "/data"},
								{Name: "COLLECT_INTERVAL_SECONDS", Value: "10"},
							},
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080, Name: "metrics"},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "vm-data",
									MountPath: "/data",
									ReadOnly:  true,
								},
							},
						},
					},
				},
			},
		},
	}

	_, err = p.clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, v1.CreateOptions{})
	return err
}

func (p *provider) WaitForLokiSingleBinaryDataPVC(ctx context.Context, namespace string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		pvcName, err := p.lookupLokiSingleBinaryDataPVC(ctx, namespace)
		if err == nil {
			return pvcName, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return "", fmt.Errorf("wait loki single-binary data PVC in %q: %w", namespace, lastErr)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (p *provider) lookupLokiSingleBinaryDataPVC(ctx context.Context, namespace string) (string, error) {
	list, err := p.clientset.AppsV1().StatefulSets(namespace).List(ctx, v1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=loki,app.kubernetes.io/component=single-binary",
	})
	if err != nil {
		return "", err
	}
	if len(list.Items) == 0 {
		return "", fmt.Errorf("statefulset with app.kubernetes.io/component=single-binary not found")
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
	stsName := list.Items[0].Name
	pvcName := "storage-" + stsName + "-0"
	_, err = p.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, v1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return "", fmt.Errorf("pvc %q does not exist yet", pvcName)
		}
		return "", err
	}
	return pvcName, nil
}

// readyPodsSorted фильтрует Running+Ready поды и сортирует по имени.
func readyPodsSorted(pods *corev1.PodList) []corev1.Pod {
	var out []corev1.Pod
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				out = append(out, pod)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func resolvePortForwardTarget(svc *corev1.Service, pod *corev1.Pod, fallback int) int {
	var sp *corev1.ServicePort
	for i := range svc.Spec.Ports {
		p := &svc.Spec.Ports[i]
		if p.Protocol == corev1.ProtocolUDP {
			continue
		}
		if p.Name == "http" {
			sp = p
			break
		}
		if sp == nil {
			sp = p
		}
	}
	if sp == nil {
		return fallback
	}
	tp := sp.TargetPort
	switch tp.Type {
	case intstr.Int:
		if tp.IntVal > 0 {
			return int(tp.IntVal)
		}
	case intstr.String:
		if tp.StrVal != "" {
			if pnum := containerPortNumberByName(pod, tp.StrVal); pnum > 0 {
				return pnum
			}
		}
	}
	return fallback
}

func containerPortNumberByName(pod *corev1.Pod, portName string) int {
	for _, c := range pod.Spec.Containers {
		for _, cp := range c.Ports {
			if cp.Name == portName {
				return int(cp.ContainerPort)
			}
		}
	}
	return 0
}

func formatIntOrString(tp intstr.IntOrString) string {
	switch tp.Type {
	case intstr.Int:
		return strconv.Itoa(int(tp.IntVal))
	case intstr.String:
		return tp.StrVal
	default:
		return ""
	}
}

func logPortForwardDiagnostics(ctx context.Context, namespace, serviceName string, svc *corev1.Service, pod *corev1.Pod, localPort, targetPort, configuredRemote int) {
	for _, p := range svc.Spec.Ports {
		if p.Protocol == corev1.ProtocolUDP {
			continue
		}
		slog.InfoContext(ctx, "port-forward service port",
			"namespace", namespace, "service", serviceName,
			"portName", p.Name, "servicePort", p.Port,
			"targetPort", formatIntOrString(p.TargetPort), "protocol", p.Protocol)
	}
	for _, cs := range pod.Status.ContainerStatuses {
		args := []any{
			"namespace", namespace, "pod", pod.Name, "container", cs.Name,
			"ready", cs.Ready, "restarts", cs.RestartCount,
		}
		if w := cs.State.Waiting; w != nil {
			args = append(args, "waitingReason", w.Reason, "waitingMessage", w.Message)
		}
		if r := cs.State.Running; r != nil {
			args = append(args, "startedAt", r.StartedAt.Time)
		}
		if t := cs.LastTerminationState.Terminated; t != nil {
			args = append(args,
				"lastTerminatedReason", t.Reason,
				"lastExitCode", t.ExitCode,
				"lastFinishedAt", t.FinishedAt.Time,
			)
		}
		slog.InfoContext(ctx, "port-forward pod container status", args...)
	}
	slog.InfoContext(ctx, "port-forward starting",
		"namespace", namespace, "service", serviceName, "pod", pod.Name, "podUID", pod.UID,
		"localPort", localPort, "targetPort", targetPort, "topologyRemotePort", configuredRemote)
}

func (p *provider) PortForwardService(ctx context.Context, namespace, serviceName string, localPort, remotePort int) (chan struct{}, error) {
	svc, err := p.getServiceWithWait(ctx, namespace, serviceName, 2*time.Minute)
	if err != nil {
		return nil, err
	}

	var selectorParts []string
	for k, val := range svc.Spec.Selector {
		selectorParts = append(selectorParts, k+"="+val)
	}
	if len(selectorParts) == 0 {
		return nil, fmt.Errorf("service %s/%s has empty spec.selector, cannot select pods for port-forward", namespace, serviceName)
	}

	var chosen corev1.Pod
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		pods, err := p.clientset.CoreV1().Pods(namespace).List(ctx, v1.ListOptions{
			LabelSelector: strings.Join(selectorParts, ","),
		})
		if err == nil {
			ready := readyPodsSorted(pods)
			if len(ready) > 0 {
				chosen = ready[0]
				break
			}
		}
		time.Sleep(2 * time.Second)
	}

	targetPort := resolvePortForwardTarget(svc, &chosen, remotePort)
	if targetPort != remotePort {
		slog.InfoContext(ctx, "port-forward resolved target port from service",
			"namespace", namespace, "service", serviceName, "pod", chosen.Name,
			"resolvedPort", targetPort, "configuredPort", remotePort)
	}
	logPortForwardDiagnostics(ctx, namespace, serviceName, svc, &chosen, localPort, targetPort, remotePort)

	url := p.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(chosen.Name).
		SubResource("portforward").
		URL()

	transport, upgrader, err := spdy.RoundTripperFor(p.restConfig)
	if err != nil {
		return nil, err
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, url)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	fw, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", localPort, targetPort)}, stopCh, readyCh, &slogWriter{}, &slogWriter{})
	if err != nil {
		return nil, err
	}

	errCh := make(chan error, 1)
	go func() {
		err := fw.ForwardPorts()
		if err != nil {
			slog.ErrorContext(ctx, "port-forward tunnel closed",
				"err", err,
				"namespace", namespace, "pod", chosen.Name,
				"local_to_target", fmt.Sprintf("%d->%d", localPort, targetPort))
		}
		errCh <- err
	}()

	select {
	case <-readyCh:
		return stopCh, nil
	case err := <-errCh:
		return nil, fmt.Errorf("port-forward failed: %w", err)
	case <-ctx.Done():
		close(stopCh)
		return nil, ctx.Err()
	}
}

func (p *provider) getServiceWithWait(ctx context.Context, namespace, serviceName string, waitFor time.Duration) (*corev1.Service, error) {
	var err error

	ctx, cancel := context.WithTimeout(ctx, waitFor)
	defer cancel()

	for {
		svc, localErr := p.clientset.CoreV1().Services(namespace).Get(ctx, serviceName, v1.GetOptions{})
		if localErr != nil {
			err = localErr
		} else {
			return svc, nil
		}

		select {
		case <-ctx.Done():
			return nil, err
		default:
		}

		time.Sleep(1 * time.Second)
	}
}
