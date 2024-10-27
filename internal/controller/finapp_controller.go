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
	"github.com/go-logr/logr"
	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	errapi "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sync"
	tencentcloudv1beta1 "tencentcloud.com/finopscontroller/api/v1beta1"
)

const (
	finalizerName = "finopscontroller.tencentcloud.com/finalizer"
	hashLength    = 8
	cronJobImage  = "swr.cn-north-4.myhuaweicloud.com/ddn-k8s/gcr.io/google-containers/alpine-with-bash:1.0"
)

var (
	logger      logr.Logger
	cronJobARGS = []string{"/bin/sh", "-c", "echo 'Running tests...' && sleep 30"}
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
// the FinApp object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *FinAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger = log.FromContext(ctx)
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
	// need to resync status ?
	statusSync, updateFinApp := r.needToSyncStatus(ctx, req, finApp)
	if statusSync {
		logger.V(1).Info("FinApp status sync", "finApp", req.NamespacedName)
		if err := r.Status().Update(ctx, updateFinApp); err != nil {
			logger.Error(err, "unable to update finApp status")
			return ctrl.Result{}, err
		}
		// we do early ret to make logic clear
		return ctrl.Result{}, nil
	}
	// try to Reconcile resources
	if err := r.reconcileResources(ctx, req, finApp); err != nil {
		logger.Error(err, "unable to reconcile resources")
		return ctrl.Result{}, err
	}
	logger.V(1).Info("FinApp reconcile success")
	return ctrl.Result{}, nil
}

// needToSyncStatus check if status should change
// in our cases:
// 1. cronjob is changed by some reason
// 2. we need to update our predict result (Predictions,LastPredictionTime) by resync
// please read https://github.com/cloudnativeto/sig-kubernetes/issues/11 if you want to know more about resync in informer
// Return true: status need to be updated; false: current status is right,try to check reconcileResources
func (r *FinAppReconciler) needToSyncStatus(ctx context.Context, req ctrl.Request, finApp *tencentcloudv1beta1.FinApp) (bool, *tencentcloudv1beta1.FinApp) {
	// we can check these in the same time
	var wg sync.WaitGroup
	cronjobUpdate, predictionUpdate := false, false
	mutex := &sync.Mutex{}
	wg.Add(2)
	// check cronjob
	go func() {
		defer mutex.Unlock()
		status := r.checkCronjob(ctx, req, finApp, &wg)
		mutex.Lock()
		if status != finApp.Status.Cronjob {
			cronjobUpdate = true
			finApp.Status.Cronjob = status
		}
	}()
	go func() {
		// todo
		defer mutex.Unlock()
		status := r.checkPredictConf(ctx, req, &wg)
		mutex.Lock()
		if status {
			predictionUpdate = true
		}
	}()
	wg.Wait()
	logger.V(1).Info("status predict success", "cronjobUpdate", cronjobUpdate, "predictionUpdate", predictionUpdate, "status", finApp.Status)
	return cronjobUpdate || predictionUpdate, finApp
}

// reconcileResources
func (r *FinAppReconciler) reconcileResources(ctx context.Context, req ctrl.Request, finApp *tencentcloudv1beta1.FinApp) error {
	logger.V(1).Info("start to Reconciling FinApp resources")
	if finApp.Status.Cronjob != true {
		if err := r.reconcileCronJob(ctx, req, finApp); err != nil {
			logger.Error(err, "unable to reconcile cronjob")
			return err
		}
		logger.V(1).Info("finish to Reconciling FinApp resources")
	}
	if finApp.Status.LastPredictionTime.IsZero() || finApp.Status.Predictions == nil {

	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FinAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tencentcloudv1beta1.FinApp{}).
		Owns(&v1.CronJob{}).
		Complete(r)
}

func (r *FinAppReconciler) reconcileCronJob(ctx context.Context, req ctrl.Request, finApp *tencentcloudv1beta1.FinApp) error {
	logger.V(1).Info("start to Reconcile CronJob")
	// get cronjob
	cronJob := &v1.CronJob{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace,
		Name: finApp.Name + "-cronjob-" + hashSpec(finApp.Spec)[:hashLength]}, cronJob); err != nil {
		if errapi.IsNotFound(err) {
			logger.V(1).Info("FinApp cronjob not found,try to create", "FinApp", req.NamespacedName)
			if err = r.createPredictionCronJob(ctx, finApp); err != nil {
				logger.Error(err, "unable to create prediction cronjob")
				return err
			}
			finApp.Status.Cronjob = true
			if err = r.Status().Update(ctx, finApp); err != nil {
				logger.Error(err, "unable to update finApp status")
				return err
			}
			logger.V(1).Info("FinApp cronjob created", "FinApp", req.NamespacedName)
			return nil
		}
		logger.Error(err, "unable to fetch finapp cronjob")
		return err
	}
	// Reconcile Cronjob status
	cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0] = corev1.Container{
		Name:  "predict-cronjob-container",
		Image: cronJobImage,
		Args:  cronJobARGS,
	}
	if err := r.Status().Update(ctx, cronJob); err != nil {
		logger.Error(err, "unable to update finApp status")
		return err
	}
	finApp.Status.Cronjob = true
	if err := r.Update(ctx, finApp); err != nil {
		logger.Error(err, "unable to update finApp status")
		return err
	}
	logger.V(1).Info("FinApp cronjob sync success")
	return nil
}

func (r *FinAppReconciler) createPredictionCronJob(ctx context.Context, finApp *tencentcloudv1beta1.FinApp) error {
	logger.V(1).Info("Creating prediction cronjob")
	if finApp == nil {
		return fmt.Errorf("finApp  is nil")
	}
	// create the prediction cronjob
	cronJob := &v1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      finApp.Name + "-cronjob-" + hashSpec(finApp.Spec)[:hashLength], // CronJob 名称
			Namespace: finApp.Namespace,
			// avoid deleting cronjob by accident
			//Finalizers: []string{finalizerName},
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
									Image: cronJobImage,
									Args:  cronJobARGS,
								},
							},
							RestartPolicy: corev1.RestartPolicyOnFailure,
						},
					},
				},
			},
		},
	}
	// set OwnerReference
	if err := controllerutil.SetControllerReference(finApp, cronJob, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, cronJob); err != nil {
		logger.Error(err, "unable to create prediction cronjob", "reason:", err.Error())
		return err
	}
	return nil
}

// checkCronjob indicts cronjob current status
func (r *FinAppReconciler) checkCronjob(ctx context.Context, req ctrl.Request, finApp *tencentcloudv1beta1.FinApp, wg *sync.WaitGroup) bool {
	defer wg.Done()
	cronjob := &v1.CronJob{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: finApp.Name + "-cronjob-" + hashSpec(finApp.Spec)[:hashLength]}, cronjob); err != nil {
		if errapi.IsNotFound(err) {
			logger.V(1).Info("FinApp cronjob not found", "cronjob", req.NamespacedName)
			return false
		}
		return false
	}
	// current we check cronjob
	// 1. deleted 2. image change 3.start args change
	predictStatus := true //  predictStatus indicts the currentStatus at now
	ctr := cronjob.Spec.JobTemplate.Spec.Template.Spec.Containers
	if len(ctr) == 0 {
		predictStatus = false
	}
	if !cronjob.ObjectMeta.DeletionTimestamp.IsZero() ||
		ctr[0].Image != cronJobImage ||
		!reflect.DeepEqual(ctr[0].Args, cronJobARGS) {
		predictStatus = false
	}
	return predictStatus
}

// checkPredictConf todo
func (r *FinAppReconciler) checkPredictConf(ctx context.Context, req ctrl.Request, wg *sync.WaitGroup) bool {
	defer wg.Done()
	return false
}

// 计算字符串的 SHA256 哈希
func hashSpec(spec tencentcloudv1beta1.FinAppSpec) string {
	// 将 spec 转换为字节数组
	data, _ := json.Marshal(spec)
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
