/*
Copyright 2019 The Crossplane Authors.

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

package mysqlservervirtualnetworkrule

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplaneio/stack-azure/apis/database/v1alpha2"
	azurev1alpha2 "github.com/crossplaneio/stack-azure/apis/v1alpha2"
	azure "github.com/crossplaneio/stack-azure/pkg/clients"

	runtimev1alpha1 "github.com/crossplaneio/crossplane-runtime/apis/core/v1alpha1"
	"github.com/crossplaneio/crossplane-runtime/pkg/meta"
	"github.com/crossplaneio/crossplane-runtime/pkg/resource"
)

// Error strings.
const (
	errNewClient                           = "cannot create new MysqlServerVirtualNetworkRule"
	errNotMysqlServerVirtualNetworkRule    = "managed resource is not an MysqlServerVirtualNetworkRule"
	errCreateMysqlServerVirtualNetworkRule = "cannot create MysqlServerVirtualNetworkRule"
	errUpdateMysqlServerVirtualNetworkRule = "cannot update MysqlServerVirtualNetworkRule"
	errGetMysqlServerVirtualNetworkRule    = "cannot get MysqlServerVirtualNetworkRule"
	errDeleteMysqlServerVirtualNetworkRule = "cannot delete MysqlServerVirtualNetworkRule"
)

// Controller is responsible for adding the MysqlServerVirtualNetworkRule
// Controller and its corresponding reconciler to the manager with any runtime configuration.
type Controller struct{}

// SetupWithManager creates a new MysqlServerVirtualNetworkRule Controller and adds it to the
// Manager with default RBAC. The Manager will set fields on the Controller and
// start it when the Manager is Started.
func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	r := resource.NewManagedReconciler(mgr,
		resource.ManagedKind(v1alpha2.MysqlServerVirtualNetworkRuleGroupVersionKind),
		resource.WithManagedConnectionPublishers(),
		resource.WithExternalConnecter(&connecter{client: mgr.GetClient()}))

	name := strings.ToLower(fmt.Sprintf("%s.%s", v1alpha2.MysqlServerVirtualNetworkRuleKind, v1alpha2.Group))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha2.MysqlServerVirtualNetworkRule{}).
		Complete(r)
}

type connecter struct {
	client      client.Client
	newClientFn func(ctx context.Context, credentials []byte) (azure.MySQLVirtualNetworkRulesClient, error)
}

func (c *connecter) Connect(ctx context.Context, mg resource.Managed) (resource.ExternalClient, error) {
	v, ok := mg.(*v1alpha2.MysqlServerVirtualNetworkRule)
	if !ok {
		return nil, errors.New(errNotMysqlServerVirtualNetworkRule)
	}

	p := &azurev1alpha2.Provider{}
	n := meta.NamespacedNameOf(v.Spec.ProviderReference)
	if err := c.client.Get(ctx, n, p); err != nil {
		return nil, errors.Wrapf(err, "cannot get provider %s", n)
	}

	s := &corev1.Secret{}
	n = types.NamespacedName{Namespace: p.Spec.Secret.Namespace, Name: p.Spec.Secret.Name}
	if err := c.client.Get(ctx, n, s); err != nil {
		return nil, errors.Wrapf(err, "cannot get provider secret %s", n)
	}
	newClientFn := azure.NewMySQLVirtualNetworkRulesClient
	if c.newClientFn != nil {
		newClientFn = c.newClientFn
	}
	client, err := newClientFn(ctx, s.Data[p.Spec.Secret.Key])
	return &external{client: client}, errors.Wrap(err, errNewClient)
}

type external struct {
	client azure.MySQLVirtualNetworkRulesClient
}

func (e *external) Observe(ctx context.Context, mg resource.Managed) (resource.ExternalObservation, error) {
	v, ok := mg.(*v1alpha2.MysqlServerVirtualNetworkRule)
	if !ok {
		return resource.ExternalObservation{}, errors.New(errNotMysqlServerVirtualNetworkRule)
	}

	az, err := e.client.Get(ctx, v.Spec.ResourceGroupName, v.Spec.ServerName, v.Spec.Name)
	if azure.IsNotFound(err) {
		return resource.ExternalObservation{ResourceExists: false}, nil
	}
	if err != nil {
		return resource.ExternalObservation{}, errors.Wrap(err, errGetMysqlServerVirtualNetworkRule)
	}

	v.Status = azure.MySQLVirtualNetworkRuleStatusFromAzure(az)
	v.SetConditions(runtimev1alpha1.Available())

	o := resource.ExternalObservation{
		ResourceExists:    true,
		ConnectionDetails: resource.ConnectionDetails{},
	}

	return o, nil
}

func (e *external) Create(ctx context.Context, mg resource.Managed) (resource.ExternalCreation, error) {
	v, ok := mg.(*v1alpha2.MysqlServerVirtualNetworkRule)
	if !ok {
		return resource.ExternalCreation{}, errors.New(errNotMysqlServerVirtualNetworkRule)
	}

	v.SetConditions(runtimev1alpha1.Creating())

	vnet := azure.NewMySQLVirtualNetworkRuleParameters(v)
	if _, err := e.client.CreateOrUpdate(ctx, v.Spec.ResourceGroupName, v.Spec.ServerName, v.Spec.Name, vnet); err != nil {
		return resource.ExternalCreation{}, errors.Wrap(err, errCreateMysqlServerVirtualNetworkRule)
	}

	return resource.ExternalCreation{}, nil
}

func (e *external) Update(ctx context.Context, mg resource.Managed) (resource.ExternalUpdate, error) {
	v, ok := mg.(*v1alpha2.MysqlServerVirtualNetworkRule)
	if !ok {
		return resource.ExternalUpdate{}, errors.New(errNotMysqlServerVirtualNetworkRule)
	}

	az, err := e.client.Get(ctx, v.Spec.ResourceGroupName, v.Spec.ServerName, v.Spec.Name)
	if err != nil {
		return resource.ExternalUpdate{}, errors.Wrap(err, errGetMysqlServerVirtualNetworkRule)
	}

	if azure.MySQLServerVirtualNetworkRuleNeedsUpdate(v, az) {
		vnet := azure.NewMySQLVirtualNetworkRuleParameters(v)
		if _, err := e.client.CreateOrUpdate(ctx, v.Spec.ResourceGroupName, v.Spec.ServerName, v.Spec.Name, vnet); err != nil {
			return resource.ExternalUpdate{}, errors.Wrap(err, errUpdateMysqlServerVirtualNetworkRule)
		}
	}
	return resource.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg resource.Managed) error {
	v, ok := mg.(*v1alpha2.MysqlServerVirtualNetworkRule)
	if !ok {
		return errors.New(errNotMysqlServerVirtualNetworkRule)
	}

	v.SetConditions(runtimev1alpha1.Deleting())

	_, err := e.client.Delete(ctx, v.Spec.ResourceGroupName, v.Spec.ServerName, v.Spec.Name)

	return errors.Wrap(resource.Ignore(azure.IsNotFound, err), errDeleteMysqlServerVirtualNetworkRule)
}
