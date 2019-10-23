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

package database

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/mysql/mgmt/2017-12-01/mysql"
	"github.com/Azure/go-autorest/autorest"
	azurerest "github.com/Azure/go-autorest/autorest/azure"
	"github.com/google/go-cmp/cmp"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	runtimev1alpha1 "github.com/crossplaneio/crossplane-runtime/apis/core/v1alpha1"
	"github.com/crossplaneio/crossplane-runtime/pkg/test"

	azureclients "github.com/crossplaneio/stack-azure/pkg/clients"

	azuredbv1alpha2 "github.com/crossplaneio/stack-azure/apis/database/v1alpha2"
)

type mockSQLServerClient struct {
	MockGetServer                func(ctx context.Context, instance azuredbv1alpha2.SQLServer) (*azureclients.SQLServer, error)
	MockCreateServerBegin        func(ctx context.Context, instance azuredbv1alpha2.SQLServer, adminPassword string) ([]byte, error)
	MockCreateServerEnd          func(createOp []byte) (bool, error)
	MockDeleteServer             func(ctx context.Context, instance azuredbv1alpha2.SQLServer) (azurerest.Future, error)
	MockGetFirewallRule          func(ctx context.Context, instance azuredbv1alpha2.SQLServer, firewallRuleName string) error
	MockCreateFirewallRulesBegin func(ctx context.Context, instance azuredbv1alpha2.SQLServer, firewallRuleName string) ([]byte, error)
	MockCreateFirewallRulesEnd   func(createOp []byte) (bool, error)
}

func (m *mockSQLServerClient) GetServer(ctx context.Context, instance azuredbv1alpha2.SQLServer) (*azureclients.SQLServer, error) {
	if m.MockGetServer != nil {
		return m.MockGetServer(ctx, instance)
	}
	return &azureclients.SQLServer{}, nil
}

func (m *mockSQLServerClient) CreateServerBegin(ctx context.Context, instance azuredbv1alpha2.SQLServer, adminPassword string) ([]byte, error) {
	if m.MockCreateServerBegin != nil {
		return m.MockCreateServerBegin(ctx, instance, adminPassword)
	}
	return nil, nil
}

func (m *mockSQLServerClient) CreateServerEnd(createOp []byte) (bool, error) {
	if m.MockCreateServerEnd != nil {
		return m.MockCreateServerEnd(createOp)
	}
	return true, nil
}

func (m *mockSQLServerClient) DeleteServer(ctx context.Context, instance azuredbv1alpha2.SQLServer) (azurerest.Future, error) {
	if m.MockDeleteServer != nil {
		return m.MockDeleteServer(ctx, instance)
	}
	return azurerest.Future{}, nil
}

func (m *mockSQLServerClient) GetFirewallRule(ctx context.Context, instance azuredbv1alpha2.SQLServer, firewallRuleName string) error {
	if m.MockGetFirewallRule != nil {
		return m.MockGetFirewallRule(ctx, instance, firewallRuleName)
	}
	return nil
}

func (m *mockSQLServerClient) CreateFirewallRulesBegin(ctx context.Context, instance azuredbv1alpha2.SQLServer, firewallRuleName string) ([]byte, error) {
	if m.MockCreateFirewallRulesBegin != nil {
		return m.MockCreateFirewallRulesBegin(ctx, instance, firewallRuleName)
	}
	return nil, nil
}

func (m *mockSQLServerClient) CreateFirewallRulesEnd(createOp []byte) (bool, error) {
	if m.MockCreateFirewallRulesEnd != nil {
		return m.MockCreateFirewallRulesEnd(createOp)
	}
	return true, nil
}

type mockSQLServerClientFactory struct {
	mockClient *mockSQLServerClient
}

func (m *mockSQLServerClientFactory) CreateAPIInstance(_ *azureclients.Client) (azureclients.SQLServerAPI, error) {
	return m.mockClient, nil
}

// TestReconcile function tests the reconciliation for the full lifecycle of an Azure SQL Server instance.
// In this test, we specifically use the MySQLServer type, but the underlying reconciliation logic for the
// generic SQL Server is getting exercised (which also covers PostgreSQL)
func TestReconcile(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	sqlServerClient := &mockSQLServerClient{}
	sqlServerClientFactory := &mockSQLServerClientFactory{mockClient: sqlServerClient}

	getCallCount := 0
	sqlServerClient.MockGetServer = func(ctx context.Context, instance azuredbv1alpha2.SQLServer) (*azureclients.SQLServer, error) {
		getCallCount++
		if getCallCount <= 1 {
			// first GET should return not found, which will cause the reconcile loop to try to create the instance
			return nil, autorest.DetailedError{StatusCode: http.StatusNotFound}
		}
		// subsequent GET calls should return the created instance
		instanceName := instance.GetName()
		return &azureclients.SQLServer{
			State: string(mysql.ServerStateReady),
			ID:    instanceName + "-azure-id",
			FQDN:  instanceName + ".mydomain.azure.msft.com",
		}, nil
	}
	sqlServerClient.MockCreateServerBegin = func(ctx context.Context, instance azuredbv1alpha2.SQLServer, adminPassword string) ([]byte, error) {
		return []byte("mocked marshalled create future"), nil
	}

	getFirewallCallCount := 0
	sqlServerClient.MockGetFirewallRule = func(ctx context.Context, instance azuredbv1alpha2.SQLServer, firewallRuleName string) error {
		getFirewallCallCount++
		if getFirewallCallCount <= 1 {
			// first GET should return not found, which will cause the reconcile loop to try to create the firewall rule
			return autorest.DetailedError{StatusCode: http.StatusNotFound}
		}
		return nil
	}
	sqlServerClient.MockCreateFirewallRulesBegin = func(ctx context.Context, instance azuredbv1alpha2.SQLServer, firewallRuleName string) ([]byte, error) {
		return []byte("mocked marshalled firewall create future"), nil
	}

	// Setup the Manager and Controller.  Wrap the Controller Reconcile function so it writes each request to a
	// channel when it is finished.
	mgr, err := manager.New(cfg, manager.Options{MetricsBindAddress: ":8082"})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	c := mgr.GetClient()

	r := NewMysqlServerReconciler(mgr, sqlServerClientFactory)
	recFn, requests := SetupTestReconcile(r)
	controller := &MysqlServerController{
		Reconciler: recFn,
	}
	g.Expect(controller.SetupWithManager(mgr)).NotTo(gomega.HaveOccurred())
	defer close(StartTestManager(mgr, g))

	// create the provider object and defer its cleanup
	provider := testProvider(testSecret([]byte("testdata")))
	err = c.Create(ctx, provider)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	defer c.Delete(ctx, provider)

	// Create the SQL Server object and defer its clean up
	instance := testInstance(provider)
	err = c.Create(ctx, instance)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	defer cleanupSQLServer(t, g, c, requests, instance)

	// 1st reconcile loop should start the create operation
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))

	// after the first reconcile, the create operation should be saved on the running operation field
	expectedStatus := azuredbv1alpha2.SQLServerStatus{
		RunningOperation:     "mocked marshalled create future",
		RunningOperationType: azuredbv1alpha2.OperationCreateServer,
	}
	expectedStatus.SetConditions(runtimev1alpha1.Creating(), runtimev1alpha1.ReconcileSuccess())
	assertSQLServerStatus(g, c, expectedStatus)

	// 2nd reconcile should finish the create server operation and clear out the running operation field
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))
	expectedStatus = azuredbv1alpha2.SQLServerStatus{
		RunningOperation: "",
	}
	expectedStatus.SetConditions(runtimev1alpha1.Creating(), runtimev1alpha1.ReconcileSuccess())
	assertSQLServerStatus(g, c, expectedStatus)

	// 5th reconcile should find the SQL Server instance from Azure and update the full status of the CRD
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))

	// verify that the CRD status was updated with details about the external SQL Server and that the
	// CRD conditions show the transition from creating to running
	expectedStatus = azuredbv1alpha2.SQLServerStatus{
		Message:    "SQL Server instance test-db-instance is ready",
		State:      "Ready",
		ProviderID: instanceName + "-azure-id",
		Endpoint:   instanceName + ".mydomain.azure.msft.com",
	}
	expectedStatus.SetConditions(runtimev1alpha1.Available(), runtimev1alpha1.ReconcileSuccess())
	assertSQLServerStatus(g, c, expectedStatus)

	// wait for the connection information to be stored in a secret, then verify it
	connectionSecret := &v1.Secret{}
	n := types.NamespacedName{
		Namespace: instance.GetNamespace(),
		Name:      instance.GetSpec().WriteConnectionSecretToReference.Name,
	}
	for range time.NewTicker(1 * time.Second).C {
		if err := c.Get(ctx, n, connectionSecret); err != nil {
			t.Logf("cannot get connection secret: %s", err)
			continue
		}
		if string(connectionSecret.Data[runtimev1alpha1.ResourceCredentialsSecretEndpointKey]) != "" {
			break
		}
		t.Logf("connection secret endpoint is empty")
	}
	assertConnectionSecret(g, c, connectionSecret)

	// verify that a finalizer was added to the CRD
	c.Get(ctx, expectedRequest.NamespacedName, instance)
	g.Expect(len(instance.Finalizers)).To(gomega.Equal(1))
	g.Expect(instance.Finalizers[0]).To(gomega.Equal(mysqlFinalizer))

	cleanupSQLServer(t, g, c, requests, instance)
}

func cleanupSQLServer(t *testing.T, g *gomega.GomegaWithT, c client.Client, requests chan reconcile.Request, instance *azuredbv1alpha2.MysqlServer) {
	deletedInstance := &azuredbv1alpha2.MysqlServer{}
	if err := c.Get(ctx, expectedRequest.NamespacedName, deletedInstance); errors.IsNotFound(err) {
		// instance has already been deleted, bail out
		return
	}

	t.Logf("cleaning up SQL Server instance %s by deleting the CRD", instance.Name)
	err := c.Delete(ctx, instance)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	// wait for the deletion timestamp to be set
	err = wait.ExponentialBackoff(test.DefaultRetry, func() (done bool, err error) {
		deletedInstance := &azuredbv1alpha2.MysqlServer{}
		c.Get(ctx, expectedRequest.NamespacedName, deletedInstance)
		if deletedInstance.DeletionTimestamp != nil {
			return true, nil
		}
		return false, nil
	})
	g.Expect(err).NotTo(gomega.HaveOccurred())

	// wait for the reconcile to happen that handles the CRD deletion
	g.Eventually(requests, timeout).Should(gomega.Receive(gomega.Equal(expectedRequest)))

	// wait for the finalizer to run and the instance to be deleted for good
	err = wait.ExponentialBackoff(test.DefaultRetry, func() (done bool, err error) {
		deletedInstance := &azuredbv1alpha2.MysqlServer{}
		if err := c.Get(ctx, expectedRequest.NamespacedName, deletedInstance); errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	g.Expect(err).NotTo(gomega.HaveOccurred())
}

func assertSQLServerStatus(g *gomega.GomegaWithT, c client.Client, expectedStatus azuredbv1alpha2.SQLServerStatus) {
	instance := &azuredbv1alpha2.MysqlServer{}
	err := c.Get(ctx, expectedRequest.NamespacedName, instance)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	// assert the expected status properties
	g.Expect(instance.Status.Message).To(gomega.Equal(expectedStatus.Message))
	g.Expect(instance.Status.State).To(gomega.Equal(expectedStatus.State))
	g.Expect(instance.Status.ProviderID).To(gomega.Equal(expectedStatus.ProviderID))
	g.Expect(instance.Status.Endpoint).To(gomega.Equal(expectedStatus.Endpoint))
	g.Expect(instance.Status.RunningOperation).To(gomega.Equal(expectedStatus.RunningOperation))
	g.Expect(instance.Status.RunningOperationType).To(gomega.Equal(expectedStatus.RunningOperationType))
	g.Expect(cmp.Diff(expectedStatus.ConditionedStatus, instance.Status.ConditionedStatus, test.EquateConditions())).Should(gomega.BeZero())
}

func assertConnectionSecret(g *gomega.GomegaWithT, c client.Client, connectionSecret *v1.Secret) {
	instance := &azuredbv1alpha2.MysqlServer{}
	err := c.Get(ctx, expectedRequest.NamespacedName, instance)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	g.Expect(string(connectionSecret.Data[runtimev1alpha1.ResourceCredentialsSecretEndpointKey])).To(gomega.Equal(instance.Status.Endpoint))
	g.Expect(string(connectionSecret.Data[runtimev1alpha1.ResourceCredentialsSecretUserKey])).To(gomega.Equal(instance.Spec.AdminLoginName + "@" + instanceName))
	g.Expect(string(connectionSecret.Data[runtimev1alpha1.ResourceCredentialsSecretPasswordKey])).NotTo(gomega.BeEmpty())
}
