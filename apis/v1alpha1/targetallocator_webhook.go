// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/open-telemetry/opentelemetry-operator/apis/v1beta1"
	"github.com/open-telemetry/opentelemetry-operator/internal/config"
	"github.com/open-telemetry/opentelemetry-operator/internal/naming"
	"github.com/open-telemetry/opentelemetry-operator/internal/rbac"
)

var (
	_ admission.CustomValidator = &TargetAllocatorWebhook{}
	_ admission.CustomDefaulter = &TargetAllocatorWebhook{}
)

// +kubebuilder:webhook:path=/mutate-opentelemetry-io-v1beta1-targetallocator,mutating=true,failurePolicy=fail,groups=opentelemetry.io,resources=targetallocators,verbs=create;update,versions=v1beta1,name=mtargetallocatorbeta.kb.io,sideEffects=none,admissionReviewVersions=v1
// +kubebuilder:webhook:verbs=create;update,path=/validate-opentelemetry-io-v1beta1-targetallocator,mutating=false,failurePolicy=fail,groups=opentelemetry.io,resources=targetallocators,versions=v1beta1,name=vtargetallocatorcreateupdatebeta.kb.io,sideEffects=none,admissionReviewVersions=v1
// +kubebuilder:webhook:verbs=delete,path=/validate-opentelemetry-io-v1beta1-targetallocator,mutating=false,failurePolicy=ignore,groups=opentelemetry.io,resources=targetallocators,versions=v1beta1,name=vtargetallocatordeletebeta.kb.io,sideEffects=none,admissionReviewVersions=v1
// +kubebuilder:object:generate=false

type TargetAllocatorWebhook struct {
	logger   logr.Logger
	cfg      config.Config
	scheme   *runtime.Scheme
	reviewer *rbac.Reviewer
}

func (w TargetAllocatorWebhook) Default(_ context.Context, obj runtime.Object) error {
	targetallocator, ok := obj.(*TargetAllocator)
	if !ok {
		return fmt.Errorf("expected an TargetAllocator, received %T", obj)
	}
	return w.defaulter(targetallocator)
}

func (w TargetAllocatorWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	otelcol, ok := obj.(*TargetAllocator)
	if !ok {
		return nil, fmt.Errorf("expected an TargetAllocator, received %T", obj)
	}
	return w.validate(ctx, otelcol)
}

func (w TargetAllocatorWebhook) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	otelcol, ok := newObj.(*TargetAllocator)
	if !ok {
		return nil, fmt.Errorf("expected an TargetAllocator, received %T", newObj)
	}
	return w.validate(ctx, otelcol)
}

func (w TargetAllocatorWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	otelcol, ok := obj.(*TargetAllocator)
	if !ok || otelcol == nil {
		return nil, fmt.Errorf("expected an TargetAllocator, received %T", obj)
	}
	return w.validate(ctx, otelcol)
}

func (w TargetAllocatorWebhook) defaulter(ta *TargetAllocator) error {
	if ta.Labels == nil {
		ta.Labels = map[string]string{}
	}

	one := int32(1)

	if ta.Spec.Replicas == nil {
		ta.Spec.Replicas = &one
	}
	// if pdb isn't provided for target allocator and it's enabled
	// using a valid strategy (consistent-hashing),
	// we set MaxUnavailable 1, which will work even if there is
	// just one replica, not blocking node drains but preventing
	// out-of-the-box from disruption generated by them with replicas > 1
	if ta.Spec.AllocationStrategy == v1beta1.TargetAllocatorAllocationStrategyConsistentHashing &&
		ta.Spec.PodDisruptionBudget == nil {
		ta.Spec.PodDisruptionBudget = &v1beta1.PodDisruptionBudgetSpec{
			MaxUnavailable: &intstr.IntOrString{
				Type:   intstr.Int,
				IntVal: 1,
			},
		}
	}

	return nil
}

func (w TargetAllocatorWebhook) validate(ctx context.Context, ta *TargetAllocator) (admission.Warnings, error) {
	// TODO: Further validate scrape configs

	warnings := admission.Warnings{}

	// validate port config
	if err := v1beta1.ValidatePorts(ta.Spec.Ports); err != nil {
		return warnings, err
	}

	// if the prometheusCR is enabled, it needs a suite of permissions to function
	if ta.Spec.PrometheusCR.Enabled {
		saname := ta.Spec.ServiceAccount
		if len(ta.Spec.ServiceAccount) == 0 {
			saname = naming.TargetAllocatorServiceAccount(ta.Name)
		}
		warnings, err := v1beta1.CheckTargetAllocatorPrometheusCRPolicyRules(ctx, w.reviewer, ta.GetNamespace(), saname)
		if err != nil || len(warnings) > 0 {
			return warnings, err
		}

		// Check to see that allowNamespaces and denyNamespaces are not both set at the same time
		if len(ta.Spec.PrometheusCR.AllowNamespaces) > 0 && len(ta.Spec.PrometheusCR.DenyNamespaces) > 0 {
			return warnings, fmt.Errorf("allowNamespaces and denyNamespaces are mutually exclusive")
		}
	}

	return warnings, nil
}

func SetupTargetAllocatorWebhook(mgr ctrl.Manager, cfg config.Config, reviewer *rbac.Reviewer) error {
	cvw := &TargetAllocatorWebhook{
		reviewer: reviewer,
		logger:   mgr.GetLogger().WithValues("handler", "TargetAllocatorWebhook", "version", "v1beta1"),
		scheme:   mgr.GetScheme(),
		cfg:      cfg,
	}
	return ctrl.NewWebhookManagedBy(mgr).
		For(&TargetAllocator{}).
		WithValidator(cvw).
		WithDefaulter(cvw).
		Complete()
}
