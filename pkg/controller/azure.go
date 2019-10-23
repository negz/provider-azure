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

package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"

	azureclients "github.com/crossplaneio/stack-azure/pkg/clients"
	computeclients "github.com/crossplaneio/stack-azure/pkg/clients/compute"
	"github.com/crossplaneio/stack-azure/pkg/controller/cache"
	"github.com/crossplaneio/stack-azure/pkg/controller/compute"
	"github.com/crossplaneio/stack-azure/pkg/controller/database"
	"github.com/crossplaneio/stack-azure/pkg/controller/database/mysqlservervirtualnetworkrule"
	"github.com/crossplaneio/stack-azure/pkg/controller/database/postgresqlservervirtualnetworkrule"
	"github.com/crossplaneio/stack-azure/pkg/controller/network/subnet"
	"github.com/crossplaneio/stack-azure/pkg/controller/network/virtualnetwork"
	"github.com/crossplaneio/stack-azure/pkg/controller/resourcegroup"
	"github.com/crossplaneio/stack-azure/pkg/controller/storage/account"
	"github.com/crossplaneio/stack-azure/pkg/controller/storage/container"
)

// Controllers passes down config and adds individual controllers to the manager.
type Controllers struct{}

// SetupWithManager adds all Azure controllers to the manager.
func (c *Controllers) SetupWithManager(mgr ctrl.Manager) error { // nolint:gocyclo
	// This function has a cyclomatic complexity greater than the threshold, but it is actually a
	// very simple function that is registering controllers in a straight-forward manner.  It does
	// not really branch or behave in a complicated way, so we are ignoring gocyclo here.

	if err := (&cache.RedisClaimController{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&cache.RedisController{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&compute.AKSClusterClaimController{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&compute.AKSClusterController{
		Reconciler: compute.NewAKSClusterReconciler(mgr, &computeclients.AKSSetupClientFactory{}),
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&database.MySQLInstanceClaimController{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&database.MysqlServerController{
		Reconciler: database.NewMysqlServerReconciler(mgr, &azureclients.MySQLServerClientFactory{}),
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&mysqlservervirtualnetworkrule.Controller{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&database.PostgreSQLInstanceClaimController{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&database.PostgresqlServerController{
		Reconciler: database.NewPostgreSQLServerReconciler(mgr, &azureclients.PostgreSQLServerClientFactory{}),
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&postgresqlservervirtualnetworkrule.Controller{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&virtualnetwork.Controller{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&subnet.Controller{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&resourcegroup.Controller{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&account.ClaimController{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&account.Controller{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&container.ClaimController{}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&container.Controller{}).SetupWithManager(mgr); err != nil {
		return err
	}
	return nil
}
