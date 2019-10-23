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

package cache

import (
	"context"
	"testing"
	"time"

	redismgmt "github.com/Azure/azure-sdk-for-go/services/redis/mgmt/2018-03-01/redis"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	runtimev1alpha1 "github.com/crossplaneio/crossplane-runtime/apis/core/v1alpha1"
	"github.com/crossplaneio/crossplane-runtime/pkg/resource"
	"github.com/crossplaneio/crossplane-runtime/pkg/test"

	azure "github.com/crossplaneio/stack-azure/pkg/clients"

	"github.com/crossplaneio/stack-azure/apis/cache/v1alpha2"
	azurev1alpha2 "github.com/crossplaneio/stack-azure/apis/v1alpha2"
	"github.com/crossplaneio/stack-azure/pkg/clients/redis"
	fakeredis "github.com/crossplaneio/stack-azure/pkg/clients/redis/fake"
)

const (
	namespace              = "cool-namespace"
	uid                    = types.UID("definitely-a-uuid")
	redisResourceName      = redis.NamePrefix + "-" + string(uid)
	redisResourceGroupName = "coolgroup"
	location               = "coolplace"
	subscription           = "totally-a-uuid"
	qualifiedName          = "/subscriptions/" + subscription + "/redisResourceGroups/" + redisResourceGroupName + "/providers/Microsoft.Cache/Redis/" + redisResourceName
	host                   = "172.16.0.1"
	port                   = 6379
	sslPort                = 6380
	enableNonSSLPort       = true
	shardCount             = 3
	skuName                = v1alpha2.SKUNameBasic
	skuFamily              = v1alpha2.SKUFamilyC
	skuCapacity            = 1

	primaryAccessKey = "sosecret"

	providerName       = "cool-azure"
	providerSecretName = "cool-azure-secret"
	providerSecretKey  = "credentials"
	providerSecretData = "definitelyjson"

	connectionSecretName = "cool-connection-secret"
)

var (
	ctx                = context.Background()
	errorBoom          = errors.New("boom")
	redisConfiguration = map[string]string{"cool": "socool"}

	provider = azurev1alpha2.Provider{
		ObjectMeta: metav1.ObjectMeta{Name: providerName},
		Spec: azurev1alpha2.ProviderSpec{
			Secret: runtimev1alpha1.SecretKeySelector{
				SecretReference: runtimev1alpha1.SecretReference{
					Namespace: namespace,
					Name:      providerSecretName,
				},
				Key: providerSecretKey,
			},
		},
	}

	providerSecret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: providerSecretName},
		Data:       map[string][]byte{providerSecretKey: []byte(providerSecretData)},
	}
)

type redisResourceModifier func(*v1alpha2.Redis)

func withConditions(c ...runtimev1alpha1.Condition) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.Status.ConditionedStatus.Conditions = c }
}

func withBindingPhase(p runtimev1alpha1.BindingPhase) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.Status.SetBindingPhase(p) }
}

func withState(s string) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.Status.State = s }
}

func withFinalizers(f ...string) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.ObjectMeta.Finalizers = f }
}

func withReclaimPolicy(p runtimev1alpha1.ReclaimPolicy) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.Spec.ReclaimPolicy = p }
}

func withResourceName(n string) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.Status.ResourceName = n }
}

func withProviderID(id string) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.Status.ProviderID = id }
}

func withEndpoint(e string) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.Status.Endpoint = e }
}

func withPort(p int) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.Status.Port = p }
}

func withSSLPort(p int) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.Status.SSLPort = p }
}

func withDeletionTimestamp(t time.Time) redisResourceModifier {
	return func(r *v1alpha2.Redis) { r.ObjectMeta.DeletionTimestamp = &metav1.Time{Time: t} }
}

func redisResource(rm ...redisResourceModifier) *v1alpha2.Redis {
	r := &v1alpha2.Redis{
		ObjectMeta: metav1.ObjectMeta{
			Name:       redisResourceName,
			UID:        uid,
			Finalizers: []string{},
		},
		Spec: v1alpha2.RedisSpec{
			ResourceSpec: runtimev1alpha1.ResourceSpec{
				ProviderReference: &corev1.ObjectReference{Namespace: namespace, Name: providerName},
				WriteConnectionSecretToReference: &runtimev1alpha1.SecretReference{
					Namespace: namespace,
					Name:      connectionSecretName,
				},
			},
			RedisParameters: v1alpha2.RedisParameters{
				ResourceGroupName:  redisResourceGroupName,
				Location:           location,
				RedisConfiguration: redisConfiguration,
				EnableNonSSLPort:   enableNonSSLPort,
				ShardCount:         shardCount,
				SKU: v1alpha2.SKUSpec{
					Name:     skuName,
					Family:   skuFamily,
					Capacity: skuCapacity,
				},
			},
		},
		Status: v1alpha2.RedisStatus{
			Endpoint:   host,
			Port:       port,
			ProviderID: qualifiedName,
		},
	}

	for _, m := range rm {
		m(r)
	}

	return r
}

// Test that our Reconciler implementation satisfies the Reconciler interface.
var _ reconcile.Reconciler = &Reconciler{}

func TestCreate(t *testing.T) {
	cases := []struct {
		name        string
		csdk        createsyncdeletekeyer
		r           *v1alpha2.Redis
		want        *v1alpha2.Redis
		wantRequeue bool
	}{
		{
			name: "SuccessfulCreate",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockCreate: func(_ context.Context, _, _ string, _ redismgmt.CreateParameters) (redismgmt.CreateFuture, error) {
					return redismgmt.CreateFuture{}, nil
				},
			}},
			r: redisResource(),
			want: redisResource(
				withConditions(runtimev1alpha1.Creating(), runtimev1alpha1.ReconcileSuccess()),
				withFinalizers(finalizerName),
				withResourceName(redisResourceName),
			),
			wantRequeue: true,
		},
		{
			name: "FailedCreate",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockCreate: func(_ context.Context, _, _ string, _ redismgmt.CreateParameters) (redismgmt.CreateFuture, error) {
					return redismgmt.CreateFuture{}, errorBoom
				},
			}},
			r: redisResource(),
			want: redisResource(
				withConditions(runtimev1alpha1.Creating(), runtimev1alpha1.ReconcileError(errorBoom)),
			),
			wantRequeue: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRequeue := tc.csdk.Create(ctx, tc.r)

			if gotRequeue != tc.wantRequeue {
				t.Errorf("tc.csdk.Create(...): want: %t got: %t", tc.wantRequeue, gotRequeue)
			}

			if diff := cmp.Diff(tc.want, tc.r, test.EquateConditions()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}
}

func TestSync(t *testing.T) {
	cases := []struct {
		name        string
		csdk        createsyncdeletekeyer
		r           *v1alpha2.Redis
		want        *v1alpha2.Redis
		wantRequeue bool
	}{
		{
			name: "SuccessfulSyncWhileResourceCreating",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockGet: func(_ context.Context, _, _ string) (redismgmt.ResourceType, error) {
					return redismgmt.ResourceType{Properties: &redismgmt.Properties{ProvisioningState: redismgmt.Creating}}, nil
				},
			}},
			r: redisResource(
				withResourceName(redisResourceName),
			),
			want: redisResource(
				withState(v1alpha2.ProvisioningStateCreating),
				withResourceName(redisResourceName),
				withConditions(runtimev1alpha1.Creating(), runtimev1alpha1.ReconcileSuccess()),
			),
			wantRequeue: true,
		},
		{
			name: "SuccessfulSyncWhileResourceDeleting",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockGet: func(_ context.Context, _, _ string) (redismgmt.ResourceType, error) {
					return redismgmt.ResourceType{Properties: &redismgmt.Properties{ProvisioningState: redismgmt.Deleting}}, nil
				},
			}},
			r: redisResource(
				withResourceName(redisResourceName),
			),
			want: redisResource(
				withResourceName(redisResourceName),
				withState(v1alpha2.ProvisioningStateDeleting),
				withConditions(runtimev1alpha1.Deleting(), runtimev1alpha1.ReconcileSuccess()),
			),
			wantRequeue: false,
		},
		{
			name: "SuccessfulSyncWhileResourceUpdating",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockGet: func(_ context.Context, _, _ string) (redismgmt.ResourceType, error) {
					return redismgmt.ResourceType{Properties: &redismgmt.Properties{ProvisioningState: redismgmt.Updating}}, nil
				},
			}},
			r: redisResource(
				withResourceName(redisResourceName),
			),
			want: redisResource(
				withResourceName(redisResourceName),
				withState(v1alpha2.ProvisioningStateUpdating),
				withConditions(runtimev1alpha1.ReconcileSuccess()),
			),
			wantRequeue: true,
		},
		{
			name: "SuccessfulSyncWhileResourceReadyAndDoesNotNeedUpdate",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockGet: func(_ context.Context, _, _ string) (redismgmt.ResourceType, error) {
					return redismgmt.ResourceType{
						ID: azure.ToStringPtr(qualifiedName),
						Properties: &redismgmt.Properties{
							ProvisioningState: redismgmt.Succeeded,
							Sku: &redismgmt.Sku{
								Name:     redismgmt.SkuName(skuName),
								Family:   redismgmt.SkuFamily(skuFamily),
								Capacity: azure.ToInt32Ptr(skuCapacity),
							},
							EnableNonSslPort:   azure.ToBoolPtr(enableNonSSLPort),
							RedisConfiguration: azure.ToStringPtrMap(redisConfiguration),
							ShardCount:         azure.ToInt32Ptr(shardCount),
							HostName:           azure.ToStringPtr(host),
							Port:               azure.ToInt32Ptr(port),
							SslPort:            azure.ToInt32Ptr(sslPort),
						},
					}, nil
				},
			}},
			r: redisResource(
				withResourceName(redisResourceName),
			),
			want: redisResource(
				withResourceName(redisResourceName),
				withState(v1alpha2.ProvisioningStateSucceeded),
				withProviderID(qualifiedName),
				withEndpoint(host),
				withPort(port),
				withSSLPort(sslPort),
				withConditions(runtimev1alpha1.Available(), runtimev1alpha1.ReconcileSuccess()),
				withBindingPhase(runtimev1alpha1.BindingPhaseUnbound),
			),
			wantRequeue: false,
		},
		{
			name: "SuccessfulSyncWhileResourceReadyAndNeedsUpdate",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockGet: func(_ context.Context, _, _ string) (redismgmt.ResourceType, error) {
					return redismgmt.ResourceType{
						ID: azure.ToStringPtr(qualifiedName),
						Properties: &redismgmt.Properties{
							ProvisioningState: redismgmt.Succeeded,
							Sku: &redismgmt.Sku{
								Name:     redismgmt.SkuName(skuName),
								Family:   redismgmt.SkuFamily(skuFamily),
								Capacity: azure.ToInt32Ptr(skuCapacity),
							},
							EnableNonSslPort:   azure.ToBoolPtr(enableNonSSLPort),
							RedisConfiguration: azure.ToStringPtrMap(redisConfiguration),
							ShardCount:         azure.ToInt32Ptr(shardCount + 1),
							HostName:           azure.ToStringPtr(host),
							Port:               azure.ToInt32Ptr(port),
							SslPort:            azure.ToInt32Ptr(sslPort),
						},
					}, nil
				},
				MockUpdate: func(_ context.Context, _, _ string, p redismgmt.UpdateParameters) (redismgmt.ResourceType, error) {
					if azure.ToInt(p.ShardCount) != shardCount {
						t.Errorf("p.ShardCount: want %d, got %d", shardCount, azure.ToInt(p.ShardCount))
					}
					return redismgmt.ResourceType{}, nil
				},
			}},
			r: redisResource(
				withResourceName(redisResourceName),
			),
			want: redisResource(
				withResourceName(redisResourceName),
				withState(v1alpha2.ProvisioningStateSucceeded),
				withProviderID(qualifiedName),
				withEndpoint(host),
				withPort(port),
				withSSLPort(sslPort),
				withConditions(runtimev1alpha1.Available(), runtimev1alpha1.ReconcileSuccess()),
				withBindingPhase(runtimev1alpha1.BindingPhaseUnbound),
			),
			wantRequeue: false,
		},
		{
			name: "FailedGet",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockGet: func(_ context.Context, _, _ string) (redismgmt.ResourceType, error) {
					return redismgmt.ResourceType{}, errorBoom
				},
			}},
			r: redisResource(
				withResourceName(redisResourceName),
			),
			want: redisResource(
				withResourceName(redisResourceName),
				withConditions(runtimev1alpha1.ReconcileError(errorBoom)),
			),
			wantRequeue: true,
		},
		{
			name: "FailedUpdate",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockGet: func(_ context.Context, _, _ string) (redismgmt.ResourceType, error) {
					return redismgmt.ResourceType{
						ID: azure.ToStringPtr(qualifiedName),
						Properties: &redismgmt.Properties{
							ProvisioningState: redismgmt.Succeeded,
							Sku: &redismgmt.Sku{
								Name:     redismgmt.SkuName(skuName),
								Family:   redismgmt.SkuFamily(skuFamily),
								Capacity: azure.ToInt32Ptr(skuCapacity),
							},
							EnableNonSslPort:   azure.ToBoolPtr(enableNonSSLPort),
							RedisConfiguration: azure.ToStringPtrMap(redisConfiguration),
							ShardCount:         azure.ToInt32Ptr(shardCount + 1),
							HostName:           azure.ToStringPtr(host),
							Port:               azure.ToInt32Ptr(port),
							SslPort:            azure.ToInt32Ptr(sslPort),
						},
					}, nil
				},
				MockUpdate: func(_ context.Context, _, _ string, _ redismgmt.UpdateParameters) (redismgmt.ResourceType, error) {
					return redismgmt.ResourceType{}, errorBoom
				},
			}},
			r: redisResource(
				withResourceName(redisResourceName),
			),
			want: redisResource(
				withResourceName(redisResourceName),
				withState(v1alpha2.ProvisioningStateSucceeded),
				withProviderID(qualifiedName),
				withEndpoint(host),
				withPort(port),
				withSSLPort(sslPort),
				withConditions(runtimev1alpha1.Available(), runtimev1alpha1.ReconcileError(errorBoom)),
				withBindingPhase(runtimev1alpha1.BindingPhaseUnbound),
			),
			wantRequeue: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRequeue := tc.csdk.Sync(ctx, tc.r)

			if gotRequeue != tc.wantRequeue {
				t.Errorf("tc.csdk.Sync(...): want: %t got: %t", tc.wantRequeue, gotRequeue)
			}

			if diff := cmp.Diff(tc.want, tc.r, test.EquateConditions()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	cases := []struct {
		name        string
		csdk        createsyncdeletekeyer
		r           *v1alpha2.Redis
		want        *v1alpha2.Redis
		wantRequeue bool
	}{
		{
			name: "ReclaimRetainSuccessfulDelete",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockDelete: func(_ context.Context, _, _ string) (redismgmt.DeleteFuture, error) {
					return redismgmt.DeleteFuture{}, nil
				},
			}},
			r: redisResource(withFinalizers(finalizerName), withReclaimPolicy(runtimev1alpha1.ReclaimRetain)),
			want: redisResource(
				withReclaimPolicy(runtimev1alpha1.ReclaimRetain),
				withConditions(runtimev1alpha1.Deleting(), runtimev1alpha1.ReconcileSuccess()),
			),
			wantRequeue: false,
		},
		{
			name: "ReclaimDeleteSuccessfulDelete",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockDelete: func(_ context.Context, _, _ string) (redismgmt.DeleteFuture, error) {
					return redismgmt.DeleteFuture{}, nil
				},
			}},
			r: redisResource(withFinalizers(finalizerName), withReclaimPolicy(runtimev1alpha1.ReclaimDelete)),
			want: redisResource(
				withReclaimPolicy(runtimev1alpha1.ReclaimDelete),
				withConditions(runtimev1alpha1.Deleting(), runtimev1alpha1.ReconcileSuccess()),
			),
			wantRequeue: false,
		},
		{
			name: "ReclaimDeleteFailedDelete",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockDelete: func(_ context.Context, _, _ string) (redismgmt.DeleteFuture, error) {
					return redismgmt.DeleteFuture{}, errorBoom
				},
			}},
			r: redisResource(withFinalizers(finalizerName), withReclaimPolicy(runtimev1alpha1.ReclaimDelete)),
			want: redisResource(
				withFinalizers(finalizerName),
				withReclaimPolicy(runtimev1alpha1.ReclaimDelete),
				withConditions(runtimev1alpha1.Deleting(), runtimev1alpha1.ReconcileError(errorBoom)),
			),
			wantRequeue: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRequeue := tc.csdk.Delete(ctx, tc.r)

			if gotRequeue != tc.wantRequeue {
				t.Errorf("tc.csdk.Delete(...): want: %t got: %t", tc.wantRequeue, gotRequeue)
			}

			if diff := cmp.Diff(tc.want, tc.r, test.EquateConditions()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}
}
func TestKey(t *testing.T) {
	cases := []struct {
		name    string
		csdk    createsyncdeletekeyer
		r       *v1alpha2.Redis
		want    *v1alpha2.Redis
		wantKey string
	}{
		{
			name: "Successful",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockListKeys: func(_ context.Context, _, _ string) (redismgmt.AccessKeys, error) {
					return redismgmt.AccessKeys{PrimaryKey: azure.ToStringPtr(primaryAccessKey)}, nil
				},
			}},
			r:       redisResource(),
			want:    redisResource(),
			wantKey: primaryAccessKey,
		},
		{
			name: "Failed",
			csdk: &azureRedisCache{client: &fakeredis.MockClient{
				MockListKeys: func(_ context.Context, _, _ string) (redismgmt.AccessKeys, error) {
					return redismgmt.AccessKeys{}, errorBoom
				},
			}},
			r:    redisResource(),
			want: redisResource(withConditions(runtimev1alpha1.ReconcileError(errorBoom))),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotKey := tc.csdk.Key(ctx, tc.r)

			if gotKey != tc.wantKey {
				t.Errorf("tc.csdk.Key(...): want: %s got: %s", tc.wantKey, gotKey)
			}

			if diff := cmp.Diff(tc.want, tc.r, test.EquateConditions()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}
}

func TestConnect(t *testing.T) {
	cases := []struct {
		name    string
		conn    connecter
		i       *v1alpha2.Redis
		want    createsyncdeletekeyer
		wantErr error
	}{
		{
			name: "SuccessfulConnect",
			conn: &providerConnecter{
				kube: &test.MockClient{MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
					switch key {
					case client.ObjectKey{Namespace: namespace, Name: providerName}:
						*obj.(*azurev1alpha2.Provider) = provider
					case client.ObjectKey{Namespace: namespace, Name: providerSecretName}:
						*obj.(*corev1.Secret) = providerSecret
					}
					return nil
				}},
				newClient: func(_ context.Context, _ []byte) (redis.Client, error) { return &fakeredis.MockClient{}, nil },
			},
			i:    redisResource(),
			want: &azureRedisCache{client: &fakeredis.MockClient{}},
		},
		{
			name: "FailedToGetProvider",
			conn: &providerConnecter{
				kube: &test.MockClient{MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
					return kerrors.NewNotFound(schema.GroupResource{}, providerName)
				}},
				newClient: func(_ context.Context, _ []byte) (redis.Client, error) { return &fakeredis.MockClient{}, nil },
			},
			i:       redisResource(),
			wantErr: errors.WithStack(errors.Errorf("cannot get provider %s/%s:  \"%s\" not found", namespace, providerName, providerName)),
		},
		{
			name: "FailedToGetProviderSecret",
			conn: &providerConnecter{
				kube: &test.MockClient{MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
					switch key {
					case client.ObjectKey{Namespace: namespace, Name: providerName}:
						*obj.(*azurev1alpha2.Provider) = provider
					case client.ObjectKey{Namespace: namespace, Name: providerSecretName}:
						return kerrors.NewNotFound(schema.GroupResource{}, providerSecretName)
					}
					return nil
				}},
				newClient: func(_ context.Context, _ []byte) (redis.Client, error) { return &fakeredis.MockClient{}, nil },
			},
			i:       redisResource(),
			wantErr: errors.WithStack(errors.Errorf("cannot get provider secret %s/%s:  \"%s\" not found", namespace, providerSecretName, providerSecretName)),
		},
		{
			name: "FailedToCreateAzureCacheClient",
			conn: &providerConnecter{
				kube: &test.MockClient{MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
					switch key {
					case client.ObjectKey{Namespace: namespace, Name: providerName}:
						*obj.(*azurev1alpha2.Provider) = provider
					case client.ObjectKey{Namespace: namespace, Name: providerSecretName}:
						*obj.(*corev1.Secret) = providerSecret
					}
					return nil
				}},
				newClient: func(_ context.Context, _ []byte) (redis.Client, error) { return nil, errorBoom },
			},
			i:       redisResource(),
			want:    &azureRedisCache{},
			wantErr: errors.Wrap(errorBoom, "cannot create new Azure Cache client"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotErr := tc.conn.Connect(ctx, tc.i)

			if diff := cmp.Diff(tc.wantErr, gotErr, test.EquateErrors()); diff != "" {
				t.Errorf("tc.conn.Connect(...): want error != got error:\n%s", diff)
			}

			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(azureRedisCache{})); diff != "" {
				t.Errorf("tc.conn.Connect(...): -want, +got:\n%s", diff)
			}
		})
	}
}

type mockConnector struct {
	MockConnect func(ctx context.Context, i *v1alpha2.Redis) (createsyncdeletekeyer, error)
}

func (c *mockConnector) Connect(ctx context.Context, i *v1alpha2.Redis) (createsyncdeletekeyer, error) {
	return c.MockConnect(ctx, i)
}

type mockCSDK struct {
	MockCreate func(ctx context.Context, i *v1alpha2.Redis) bool
	MockSync   func(ctx context.Context, i *v1alpha2.Redis) bool
	MockDelete func(ctx context.Context, i *v1alpha2.Redis) bool
	MockKey    func(ctx context.Context, i *v1alpha2.Redis) string
}

func (csdk *mockCSDK) Create(ctx context.Context, i *v1alpha2.Redis) bool {
	return csdk.MockCreate(ctx, i)
}

func (csdk *mockCSDK) Sync(ctx context.Context, i *v1alpha2.Redis) bool {
	return csdk.MockSync(ctx, i)
}

func (csdk *mockCSDK) Delete(ctx context.Context, i *v1alpha2.Redis) bool {
	return csdk.MockDelete(ctx, i)
}

func (csdk *mockCSDK) Key(ctx context.Context, i *v1alpha2.Redis) string {
	return csdk.MockKey(ctx, i)
}

func TestReconcile(t *testing.T) {
	cases := []struct {
		name    string
		rec     *Reconciler
		req     reconcile.Request
		want    reconcile.Result
		wantErr error
	}{
		{
			name: "SuccessfulDelete",
			rec: &Reconciler{
				connecter: &mockConnector{MockConnect: func(_ context.Context, _ *v1alpha2.Redis) (createsyncdeletekeyer, error) {
					return &mockCSDK{MockDelete: func(_ context.Context, _ *v1alpha2.Redis) bool { return false }}, nil
				}},
				kube: &test.MockClient{
					MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
						*obj.(*v1alpha2.Redis) = *(redisResource(withResourceName(redisResourceName), withDeletionTimestamp(time.Now())))
						return nil
					},
					MockUpdate: func(_ context.Context, _ runtime.Object, _ ...client.UpdateOption) error { return nil },
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: redisResourceName}},
			want:    reconcile.Result{Requeue: false},
			wantErr: nil,
		},
		{
			name: "SuccessfulCreate",
			rec: &Reconciler{
				connecter: &mockConnector{MockConnect: func(_ context.Context, _ *v1alpha2.Redis) (createsyncdeletekeyer, error) {
					return &mockCSDK{MockCreate: func(_ context.Context, _ *v1alpha2.Redis) bool { return true }}, nil
				}},
				kube: &test.MockClient{
					MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
						*obj.(*v1alpha2.Redis) = *(redisResource())
						return nil
					},
					MockUpdate: func(_ context.Context, _ runtime.Object, _ ...client.UpdateOption) error { return nil },
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: redisResourceName}},
			want:    reconcile.Result{Requeue: true},
			wantErr: nil,
		},
		{
			name: "SuccessfulSync",
			rec: &Reconciler{
				connecter: &mockConnector{MockConnect: func(_ context.Context, _ *v1alpha2.Redis) (createsyncdeletekeyer, error) {
					return &mockCSDK{
						MockSync: func(_ context.Context, _ *v1alpha2.Redis) bool { return false },
						MockKey:  func(_ context.Context, _ *v1alpha2.Redis) string { return "" },
					}, nil
				}},
				kube: &test.MockClient{
					MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
						*obj.(*v1alpha2.Redis) = *(redisResource(withResourceName(redisResourceName), withEndpoint(host)))
						return nil
					},
					MockUpdate: func(_ context.Context, _ runtime.Object, _ ...client.UpdateOption) error { return nil },
					MockCreate: func(_ context.Context, _ runtime.Object, _ ...client.CreateOption) error { return nil },
				},
				publisher: resource.PublisherChain{}, // A no-op publisher.
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: redisResourceName}},
			want:    reconcile.Result{Requeue: false},
			wantErr: nil,
		},
		{
			name: "FailedToGetNonexistentResource",
			rec: &Reconciler{
				kube: &test.MockClient{
					MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
						return kerrors.NewNotFound(schema.GroupResource{}, redisResourceName)
					},
					MockUpdate: func(_ context.Context, _ runtime.Object, _ ...client.UpdateOption) error { return nil },
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: redisResourceName}},
			want:    reconcile.Result{Requeue: false},
			wantErr: nil,
		},
		{
			name: "FailedToGetExtantResource",
			rec: &Reconciler{
				kube: &test.MockClient{
					MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
						return errorBoom
					},
					MockUpdate: func(_ context.Context, _ runtime.Object, _ ...client.UpdateOption) error { return nil },
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: redisResourceName}},
			want:    reconcile.Result{Requeue: false},
			wantErr: errors.Wrapf(errorBoom, "cannot get resource %s/%s", namespace, redisResourceName),
		},
		{
			name: "FailedToConnect",
			rec: &Reconciler{
				connecter: &mockConnector{MockConnect: func(_ context.Context, _ *v1alpha2.Redis) (createsyncdeletekeyer, error) {
					return nil, errorBoom
				}},
				kube: &test.MockClient{
					MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
						*obj.(*v1alpha2.Redis) = *(redisResource())
						return nil
					},
					MockUpdate: func(_ context.Context, obj runtime.Object, _ ...client.UpdateOption) error {
						want := redisResource(withConditions(runtimev1alpha1.ReconcileError(errorBoom)))
						got := obj.(*v1alpha2.Redis)
						if diff := cmp.Diff(want, got, test.EquateConditions()); diff != "" {
							t.Errorf("kube.Update(...): -want, +got:\n%s", diff)
						}
						return nil
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: redisResourceName}},
			want:    reconcile.Result{Requeue: true},
			wantErr: nil,
		},
		{
			name: "FailedToPublish",
			rec: &Reconciler{
				connecter: &mockConnector{MockConnect: func(_ context.Context, _ *v1alpha2.Redis) (createsyncdeletekeyer, error) {
					return &mockCSDK{
						MockSync: func(_ context.Context, _ *v1alpha2.Redis) bool { return false },
						MockKey:  func(_ context.Context, _ *v1alpha2.Redis) string { return "" },
					}, nil
				}},
				kube: &test.MockClient{
					MockGet: func(_ context.Context, key client.ObjectKey, obj runtime.Object) error {
						*obj.(*v1alpha2.Redis) = *(redisResource(withResourceName(redisResourceName), withEndpoint(host)))
						return nil
					},
					MockCreate: func(_ context.Context, _ runtime.Object, _ ...client.CreateOption) error { return nil },
					MockUpdate: func(_ context.Context, obj runtime.Object, _ ...client.UpdateOption) error {
						want := redisResource(
							withResourceName(redisResourceName),
							withConditions(runtimev1alpha1.ReconcileError(errorBoom)),
						)
						got := obj.(*v1alpha2.Redis)
						if diff := cmp.Diff(want, got, test.EquateConditions()); diff != "" {
							t.Errorf("kube.Update(...): -want, +got:\n%s", diff)
						}
						return nil
					},
				},
				publisher: resource.ManagedConnectionPublisherFns{
					PublishConnectionFn: func(_ context.Context, _ resource.Managed, _ resource.ConnectionDetails) error {
						return errorBoom
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: redisResourceName}},
			want:    reconcile.Result{Requeue: true},
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotResult, gotErr := tc.rec.Reconcile(tc.req)

			if diff := cmp.Diff(tc.wantErr, gotErr, test.EquateErrors()); diff != "" {
				t.Errorf("tc.rec.Reconcile(...): want error != got error:\n%s", diff)
			}

			if diff := cmp.Diff(tc.want, gotResult); diff != "" {
				t.Errorf("tc.rec.Reconcile(...): -want, +got:\n%s", diff)
			}
		})
	}
}
