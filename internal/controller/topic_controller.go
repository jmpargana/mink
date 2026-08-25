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
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	minkv1 "io.musil/mink/api/v1"
)

const (
	seederVolumeName = "seeder-cfg"
	seederMountPath  = "/tmp/seeder-cfg"
	seederConfigFile = "seeder.toml"
	maxRetries       = 3
)

// TopicReconciler reconciles a Topic object.
type TopicReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mink.io.musil,resources=topics,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mink.io.musil,resources=topics/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mink.io.musil,resources=topics/finalizers,verbs=update
// +kubebuilder:rbac:groups=mink.io.musil,resources=brokers,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// TODO: add logging throughout flow, alongside status updates.
func (r *TopicReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var topic minkv1.Topic
	if err := r.Get(ctx, req.NamespacedName, &topic); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Stop if permanently failed.
	if meta.IsStatusConditionTrue(topic.Status.Conditions, "Failed") {
		return ctrl.Result{}, nil
	}

	// Stop if already succeeded. 
	// As we don't have lifecycle to the `Topic` CRD, once it's seeded we ignore.
	if meta.IsStatusConditionTrue(topic.Status.Conditions, "Ready") {
		return ctrl.Result{}, nil
	}

	// Find matching Broker. It doesn't matter which instance. 
	// Metadata is forwarded to quorum (once controller CRD exists).
	// TODO: any broker works, but maybe that is not clear in the `brokerRef` field.
	var broker minkv1.Broker
	if err := r.Get(ctx, types.NamespacedName{
		Name:      topic.Spec.BrokerRef,
		Namespace: req.Namespace,
	}, &broker); err != nil {
		if errors.IsNotFound(err) {
			log.Info("broker not found, requeuing", "brokerRef", topic.Spec.BrokerRef)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	bootstrapServer := broker.Status.URL
	if bootstrapServer == "" {
		log.Info("broker URL not yet available, requeuing")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Reconcile seeder ConfigMap.
	seederToml := r.generateSeederToml(&topic)
	cmName := topic.Name + "-seeder-config"
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: topic.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = map[string]string{seederConfigFile: seederToml}
		return controllerutil.SetControllerReference(&topic, cm, r.Scheme)
	}); err != nil {
		log.Error(err, "failed to reconcile seeder ConfigMap")
		return ctrl.Result{}, err
	}

	// Load job if pending, otherwise create seeder to call broker.
	jobName := topic.Name + "-seeder"
	var existingJob batchv1.Job
	jobExists := true
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: topic.Namespace}, &existingJob); err != nil {
		if errors.IsNotFound(err) {
			jobExists = false
		} else {
			return ctrl.Result{}, err
		}
	}

	if jobExists {
		return r.handleExistingJob(ctx, &topic, &existingJob)
	}

	// Last step is to create a job in case this is the first reconciler is being called.
	return r.createSeederJob(ctx, &topic, jobName, cmName, bootstrapServer)
}

func (r *TopicReconciler) handleExistingJob(ctx context.Context, topic *minkv1.Topic, job *batchv1.Job) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if isJobSucceeded(job) {
		meta.SetStatusCondition(&topic.Status.Conditions, metav1.Condition{
			Type:               "Seeding",
			Status:             metav1.ConditionFalse,
			Reason:             "Completed",
			Message:            "Seeder job completed successfully",
			ObservedGeneration: topic.Generation,
			LastTransitionTime: metav1.Now(),
		})
		meta.SetStatusCondition(&topic.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Seeded",
			Message:            "Topic has been seeded into the broker",
			ObservedGeneration: topic.Generation,
			LastTransitionTime: metav1.Now(),
		})
		return ctrl.Result{}, r.Status().Update(ctx, topic)
	}

	if isJobFailed(job) {
		if topic.Status.RetryCount >= maxRetries {
			meta.SetStatusCondition(&topic.Status.Conditions, metav1.Condition{
				Type:               "Failed",
				Status:             metav1.ConditionTrue,
				Reason:             "MaxRetriesExceeded",
				Message:            fmt.Sprintf("Seeder job failed after %d retries", maxRetries),
				ObservedGeneration: topic.Generation,
				LastTransitionTime: metav1.Now(),
			})
			meta.SetStatusCondition(&topic.Status.Conditions, metav1.Condition{
				Type:               "Seeding",
				Status:             metav1.ConditionFalse,
				Reason:             "Failed",
				Message:            "Seeder permanently failed",
				ObservedGeneration: topic.Generation,
				LastTransitionTime: metav1.Now(),
			})
			return ctrl.Result{}, r.Status().Update(ctx, topic)
		}

		log.Info("seeder job failed, deleting for retry", "retryCount", topic.Status.RetryCount)
		propagation := metav1.DeletePropagationBackground
		if err := r.Delete(ctx, job, &client.DeleteOptions{
			PropagationPolicy: &propagation,
		}); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		topic.Status.RetryCount++
		if err := r.Status().Update(ctx, topic); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Job still running.
	return ctrl.Result{}, nil
}

func (r *TopicReconciler) createSeederJob(ctx context.Context, topic *minkv1.Topic, jobName, cmName, bootstrapServer string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	image := topic.Spec.SeederImage
	if image == "" {
		image = "ghcr.io/jmpargana/musil-seeder:0.1.5"
	}

	configFilePath := seederMountPath + "/" + seederConfigFile
	backoffLimit := int32(3)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: topic.Namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  "seeder",
						Image: image,
						Args:  []string{"-b", bootstrapServer, "-f", configFilePath},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      seederVolumeName,
							MountPath: seederMountPath,
							ReadOnly:  true,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: seederVolumeName,
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
							},
						},
					}},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(topic, job, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Create(ctx, job); err != nil {
		if errors.IsAlreadyExists(err) {
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
		log.Error(err, "failed to create seeder Job")
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&topic.Status.Conditions, metav1.Condition{
		Type:               "JobCreated",
		Status:             metav1.ConditionTrue,
		Reason:             "JobCreated",
		Message:            fmt.Sprintf("Seeder job %s created", jobName),
		ObservedGeneration: topic.Generation,
		LastTransitionTime: metav1.Now(),
	})
	meta.SetStatusCondition(&topic.Status.Conditions, metav1.Condition{
		Type:               "Seeding",
		Status:             metav1.ConditionTrue,
		Reason:             "InProgress",
		Message:            "Seeder job is running",
		ObservedGeneration: topic.Generation,
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Status().Update(ctx, topic); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("created seeder job", "job", jobName)
	return ctrl.Result{}, nil
}

func (r *TopicReconciler) generateSeederToml(topic *minkv1.Topic) string {
	toml := fmt.Sprintf(`[[topics]]
name = "%s"
num_partitions = %d
replication_factor = %d
assignments = []
`, topic.Spec.Name, topic.Spec.NumPartitions, topic.Spec.ReplicationFactor)
	return toml
}

func isJobSucceeded(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isJobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *TopicReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&minkv1.Topic{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ConfigMap{}).
		// TODO: Not sure if this is actually what needs to be done. Once KRaft is in place, 
		// single call will be reconciled across all nodes.
		Watches(&minkv1.Broker{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				broker := obj.(*minkv1.Broker)

				var topicList minkv1.TopicList
				if err := mgr.GetClient().List(ctx, &topicList, client.InNamespace(broker.Namespace)); err != nil {
					return nil
				}

				var requests []reconcile.Request
				for _, t := range topicList.Items {
					if t.Spec.BrokerRef == broker.Name {
						requests = append(requests, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: t.Name, Namespace: t.Namespace},
						})
					}
				}
				return requests
			},
		)).
		Named("topic").
		Complete(r)
}
