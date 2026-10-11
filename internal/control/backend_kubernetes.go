// SPDX-License-Identifier: AGPL-3.0-only
package control

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

// This deliberately exposes only named reads and version-conditional updates.
type namedConfigMaps interface {
	Get(context.Context, string, metav1.GetOptions) (*corev1.ConfigMap, error)
	Update(context.Context, *corev1.ConfigMap, metav1.UpdateOptions) (*corev1.ConfigMap, error)
}
type kubernetesBackend struct {
	gate    ha.LeaderGate
	objects map[Key]string
	timeout time.Duration
	cap     int
	client  namedConfigMaps
}

func NewKubernetesBackend(o KubernetesBackendOptions) (StateBackend, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("state: in-cluster config: %w", err)
	}
	cfg.Timeout = o.RequestTimeout
	client, err := coreclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return newKubernetesBackend(o, client.ConfigMaps(o.Namespace))
}
func newKubernetesBackend(o KubernetesBackendOptions, c namedConfigMaps) (StateBackend, error) {
	if o.Gate == nil || c == nil || o.RequestTimeout <= 0 || o.MaxDocumentBytes < 1 || o.MaxDocumentBytes > MaxStateDocumentBytes || len(validation.IsDNS1123Label(o.Namespace)) != 0 {
		return nil, fmt.Errorf("state: invalid Kubernetes backend options")
	}
	b := &kubernetesBackend{gate: o.Gate, objects: map[Key]string{}, timeout: o.RequestTimeout, cap: o.MaxDocumentBytes, client: c}
	names := map[string]bool{}
	for k, n := range o.Objects {
		if !validKey(k) || len(validation.IsDNS1123Subdomain(n)) != 0 || names[n] {
			return nil, fmt.Errorf("state: invalid or duplicate object mapping")
		}
		names[n] = true
		b.objects[k] = n
	}
	if len(b.objects) == 0 {
		return nil, fmt.Errorf("state: empty object mapping")
	}
	return b, nil
}
func (b *kubernetesBackend) get(ctx context.Context, key Key) (*corev1.ConfigMap, error) {
	n, ok := b.objects[key]
	if !ok {
		return nil, fmt.Errorf("state: unmapped key %q", key)
	}
	obj, err := b.client.Get(ctx, n, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if obj.ResourceVersion == "" {
		return nil, fmt.Errorf("state: missing resourceVersion")
	}
	if _, ok := obj.Data["document"]; ok {
		return nil, fmt.Errorf("state: document must be binaryData")
	}
	if err := b.validate(obj); err != nil {
		return nil, err
	}
	return obj, nil
}
func (b *kubernetesBackend) validate(obj *corev1.ConfigMap) error {
	if len(obj.BinaryData["document"]) > b.cap {
		return fmt.Errorf("state: document exceeds byte cap")
	}
	total := 0
	for k, v := range obj.Data {
		total += len(k) + len(v)
	}
	for k, v := range obj.BinaryData {
		total += len(k) + len(v)
	}
	if total > 1<<20 {
		return fmt.Errorf("state: ConfigMap aggregate exceeds 1 MiB")
	}
	return nil
}
func (b *kubernetesBackend) Load(ctx context.Context, key Key) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	obj, err := b.get(ctx, key)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Data: append([]byte(nil), obj.BinaryData["document"]...), Revision: Revision(obj.ResourceVersion)}, nil
}
func (b *kubernetesBackend) CompareAndSwap(ctx context.Context, key Key, rev Revision, data []byte) (Revision, error) {
	owned := append([]byte(nil), data...)
	var out Revision
	err := b.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		c, cancel := context.WithTimeout(c, b.timeout)
		defer cancel()
		if rev == "" {
			return fmt.Errorf("state: empty revision")
		}
		if len(owned) > b.cap {
			return fmt.Errorf("state: document exceeds byte cap")
		}
		obj, err := b.get(c, key)
		if err != nil {
			return err
		}
		if Revision(obj.ResourceVersion) != rev {
			return ErrConflict
		}
		obj = obj.DeepCopy()
		if obj.BinaryData == nil {
			obj.BinaryData = map[string][]byte{}
		}
		obj.BinaryData["document"] = owned
		if err := b.validate(obj); err != nil {
			return err
		}
		updated, err := b.client.Update(c, obj, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) {
			return ErrConflict
		}
		if err != nil {
			if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) || apierrors.IsInvalid(err) || apierrors.IsUnauthorized(err) {
				return err
			}
			return fmt.Errorf("%w: update response unavailable", ErrOutcomeUnknown)
		}
		if updated == nil || updated.ResourceVersion == "" {
			return ErrOutcomeUnknown
		}
		out = Revision(updated.ResourceVersion)
		return nil
	})
	return out, err
}
