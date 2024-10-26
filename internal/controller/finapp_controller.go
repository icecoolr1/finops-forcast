/*
Copyright 2024.

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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	errapi "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	tencentcloudv1beta1 "tencentcloud.com/finopscontroller/api/v1beta1"
)

const (
	FinalizerName = "finopscontroller.tencentcloud.com/finalizer"
	HashLength    = 8
)

// FinAppReconciler reconciles a FinApp object
type FinAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=tencentcloud.tencentcloud.com,resources=finapps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=tencentcloud.tencentcloud.com,resources=finapps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=tencentcloud.tencentcloud.com,resources=finapps/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the FinApp object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *FinAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Reconciling FinApp")
	// Get the FinApp instance
	finApp := &tencentcloudv1beta1.FinApp{}
	if err := r.Get(ctx, req.NamespacedName, finApp); err != nil {
		logger.Info("unable to fetch FinApp", "reason", err.Error())
		if errapi.IsNotFound(err) {
			logger.V(1).Info("FinApp not found", "finApp", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	// check if cronjob is being deleted
	cronjob := &v1.CronJob{}
	if err := r.Get(ctx, req.NamespacedName, cronjob); err != nil {
		if errapi.IsNotFound(err) {
			logger.Info("unable to fetch cronjob")
		}
		return ctrl.Result{}, err
	}
	if !finApp.ObjectMeta.DeletionTimestamp.IsZero() {
		finApp.Finalizers = nil
		logger.V(1).Info("FinApp is being deleted,remove finalizers")
		if err := r.Update(ctx, finApp); err != nil {
			logger.Error(err, "unable to update FinApp status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	// to make sure we will handle the cronjob deletion
	if finApp.Finalizers == nil {
		finApp.Finalizers = []string{FinalizerName}
		logger.V(1).Info("Adding finalizer to FinApp")
	}
	// LastPredictionTime means it's the first time to predict, let us create the prediction cronjob for it
	if finApp.Status.LastPredictionTime == nil {
		logger.V(1).Info("FinApp has no prediction history,start to create prediction cronjob!")
		// create the prediction cronjob
		if err := r.CreatePredictionCronJob(ctx, finApp); err != nil {
			logger.Error(err, "unable to create prediction cronjob")
			return ctrl.Result{}, err
		}
		finApp.Status.LastPredictionTime = &metav1.Time{Time: metav1.Now().Time}
		finApp.Status.Predictions = []tencentcloudv1beta1.ResourcePrediction{}
	}
	if err := r.Status().Update(ctx, finApp); err != nil {
		logger.Error(err, "unable to update FinApp status")
		return ctrl.Result{}, err
	}
	logger.V(1).Info("FinApp reconcile success")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FinAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tencentcloudv1beta1.FinApp{}).
		Owns(&v1.CronJob{}).
		Complete(r)
}

//func (r *FinAppReconciler) ReconcileCronJob(ctx context.Context, req ctrl.Request) error {
//	logger := log.FromContext(ctx)
//	logger.V(1).Info("Reconciling CronJob")
//	cronJob := &v1.CronJob{
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      req.Name,
//			Namespace: req.Namespace,
//		},
//	}
//
//}

func (r *FinAppReconciler) CreatePredictionCronJob(ctx context.Context, finApp *tencentcloudv1beta1.FinApp) error {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Creating prediction cronjob")
	if finApp == nil {
		return fmt.Errorf("finApp  is nil")
	}
	// create the prediction cronjob
	cronJob := &v1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      finApp.Name + "-cronjob-" + hashSpec(finApp.Spec)[:HashLength], // CronJob 名称
			Namespace: finApp.Namespace,
			// avoid deleting cronjob by accident
			Finalizers: []string{FinalizerName},
		},
		Spec: v1.CronJobSpec{
			Schedule: "*/1 * * * *", // todo it may should be started at a low usage time
			JobTemplate: v1.JobTemplateSpec{
				Spec: v1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "predict-cronjob-container",
									Image: "swr.cn-north-4.myhuaweicloud.com/ddn-k8s/gcr.io/google-containers/alpine-with-bash:1.0",
									Args:  []string{"/bin/sh", "-c", "echo 'Running tests...' && sleep 30"},
								},
							},
							RestartPolicy: corev1.RestartPolicyOnFailure,
						},
					},
				},
			},
		},
	}
	// 设置 OwnerReference
	if err := controllerutil.SetControllerReference(finApp, cronJob, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, cronJob); err != nil {
		logger.Error(err, "unable to create prediction cronjob", "reason:", err.Error())
		return err
	}
	return nil
}

// 计算字符串的 SHA256 哈希
func hashSpec(spec tencentcloudv1beta1.FinAppSpec) string {
	// 将 spec 转换为字节数组
	data, _ := json.Marshal(spec)
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
