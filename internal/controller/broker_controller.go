/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	minkv1 "io.musil/mink/api/v1"
)

const (
	brokerConfigVolume   = "broker-config"
	brokerConfigMount    = "/config"
	brokerResolvedVolume = "resolved-config"
	brokerResolvedMount  = "/etc/musil"
	brokerDataVolume     = "data"
	// default path in musil
	brokerDataMount = "/data"
)

// BrokerReconciler reconciles a Broker object.
type BrokerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mink.io.musil,resources=brokers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mink.io.musil,resources=brokers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mink.io.musil,resources=brokers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *BrokerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var broker minkv1.Broker
	if err := r.Get(ctx, req.NamespacedName, &broker); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// FIXME: Hardcode fields for now, as they are not available in spec just yet.
	replicas := int32(1)
	if broker.Spec.Replicas != nil {
		replicas = *broker.Spec.Replicas
	}
	port := broker.Spec.Port
	if port == 0 {
		port = 9092
	}
	image := broker.Spec.Image
	if image == "" {
		image = "ghcr.io/jmpargana/musil-server:0.1.5"
	}

	// We want to provide access to each individual pod, instead of hiding behind a service.
	// Why? Because partition leadership will be split across brokers, meaning the consumer and producer
	// need to know which particular pod they should connect to.
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: broker.Name, Namespace: broker.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.ClusterIP = corev1.ClusterIPNone
		svc.Spec.Selector = map[string]string{"app.kubernetes.io/name": broker.Name}
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "broker",
			Port:       port,
			TargetPort: intstr.FromInt32(port),
			Protocol:   corev1.ProtocolTCP,
		}}
		return controllerutil.SetControllerReference(&broker, svc, r.Scheme)
	}); err != nil {
		log.Error(err, "failed to reconcile Service")
		return ctrl.Result{}, err
	}

	// Each node will have a specific config.
	// TODO: generate other brokers inside the config instead of from CRD Spec.
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: broker.Name + "-config", Namespace: broker.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = make(map[string]string, replicas)
		for i := int32(0); i < replicas; i++ {
			cm.Data[fmt.Sprintf("node-%d.toml", i)] = fmt.Sprintf(`[controller]
node_id = %d
host = "0.0.0.0"
port = %d
topics = []

brokers = []
`, i, port)
		}
		return controllerutil.SetControllerReference(&broker, cm, r.Scheme)
	}); err != nil {
		log.Error(err, "failed to reconcile ConfigMap")
		return ctrl.Result{}, err
	}

	// Validate storage size before proceeding.
	if broker.Spec.StorageSize == "" {
		log.Error(fmt.Errorf("storageSize is required"), "invalid Broker spec")
		meta.SetStatusCondition(&broker.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidSpec",
			Message:            "spec.storageSize is required",
			ObservedGeneration: broker.Generation,
			LastTransitionTime: metav1.Now(),
		})
		_ = r.Status().Update(ctx, &broker)
		return ctrl.Result{}, nil
	}
	storageQty, err := resource.ParseQuantity(broker.Spec.StorageSize)
	if err != nil {
		log.Error(err, "invalid storageSize", "value", broker.Spec.StorageSize)
		meta.SetStatusCondition(&broker.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidSpec",
			Message:            fmt.Sprintf("invalid storageSize %q: %v", broker.Spec.StorageSize, err),
			ObservedGeneration: broker.Generation,
			LastTransitionTime: metav1.Now(),
		})
		_ = r.Status().Update(ctx, &broker)
		return ctrl.Result{}, nil
	}

	// Reconcile StatefulSet.
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: broker.Name, Namespace: broker.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		labels := map[string]string{"app.kubernetes.io/name": broker.Name}
		sts.Spec.ServiceName = broker.Name
		sts.Spec.Replicas = &replicas
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		sts.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType}
		sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{
			ObjectMeta: metav1.ObjectMeta{Name: brokerDataVolume},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: storageQty}},
				StorageClassName: broker.Spec.StorageClassName,
			},
		}}

		initScript := `#!/bin/sh
ordinal=$(echo $HOSTNAME | sed 's/.*-//')
cp /config/node-${ordinal}.toml /etc/musil/server.toml
`

		sts.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				InitContainers: []corev1.Container{{
					Name:    "init-config",
					Image:   image,
					Command: []string{"/bin/sh", "-c", initScript},
					VolumeMounts: []corev1.VolumeMount{
						{Name: brokerConfigVolume, MountPath: brokerConfigMount, ReadOnly: true},
						{Name: brokerResolvedVolume, MountPath: brokerResolvedMount},
					},
				}},
				Containers: []corev1.Container{{
					Name:  "broker",
					Image: image,
					Args:  []string{"--config", brokerResolvedMount + "/server.toml", "--path", brokerDataMount},
					Ports: []corev1.ContainerPort{{
						Name:          "broker",
						ContainerPort: port,
						Protocol:      corev1.ProtocolTCP,
					}},
					VolumeMounts: []corev1.VolumeMount{
						{Name: brokerResolvedVolume, MountPath: brokerResolvedMount, ReadOnly: true},
						{Name: brokerDataVolume, MountPath: brokerDataMount},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: brokerConfigVolume,
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: cm.Name},
							},
						},
					},
					{
						Name:         brokerResolvedVolume,
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
					},
				},
			},
		}
		return controllerutil.SetControllerReference(&broker, sts, r.Scheme)
	}); err != nil {
		log.Error(err, "failed to reconcile StatefulSet")
		return ctrl.Result{}, err
	}

	// Update status based on StatefulSet readiness.
	url := ""
	if sts.Status.ReadyReplicas > 0 {
		url = fmt.Sprintf("%s.%s.svc.cluster.local:%d", svc.Name, broker.Namespace, port)
	}

	// Without this, Topic controller won't be able to seed broker.
	broker.Status.URL = url
	condition := metav1.Condition{
		Type:               "Ready",
		ObservedGeneration: broker.Generation,
		LastTransitionTime: metav1.Now(),
	}
	if url != "" {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "StatefulSetReady"
		condition.Message = "At least one broker replica is ready"
	} else {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "WaitingForReplicas"
		condition.Message = "StatefulSet has no ready replicas yet"
	}
	meta.SetStatusCondition(&broker.Status.Conditions, condition)

	if err := r.Status().Update(ctx, &broker); err != nil {
		log.Error(err, "failed to update Broker status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BrokerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&minkv1.Broker{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Named("broker").
		Complete(r)
}
