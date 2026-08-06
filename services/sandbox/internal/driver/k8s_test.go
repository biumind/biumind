package driver

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// markRunning installs a reactor on the fake client so every newly
// created Pod is immediately marked Phase=Running. Our Create() polls
// for that state; the real cluster takes ms-to-seconds, but the fake
// stays in Pending forever unless we patch the status.
func markRunning(t *testing.T, cs *fake.Clientset) {
	t.Helper()
	cs.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		ca := a.(k8stesting.CreateAction)
		pod := ca.GetObject().(*corev1.Pod)
		pod.Status.Phase = corev1.PodRunning
		return false, pod, nil
	})
}

func TestK8sCreate_HappyPath(t *testing.T) {
	cs := fake.NewSimpleClientset()
	markRunning(t, cs)
	d := NewK8sWithClient(cs, nil, "biumind-sandbox")

	sb, err := d.Create(context.Background(), CreateInput{
		OwnerID:   "u-1",
		Image:     "alpine:3.20",
		CPUShares: 500,
		MemoryMB:  128,
		Label:     "smoke",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sb.OwnerID != "u-1" || sb.Status != "running" {
		t.Errorf("bad sandbox: %+v", sb)
	}
	pod, err := cs.CoreV1().Pods("biumind-sandbox").Get(
		context.Background(), sb.ID, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if pod.Labels[labelOwner] != "u-1" {
		t.Errorf("owner label missing: %+v", pod.Labels)
	}
	if pod.Labels[labelSandbox] != sb.ID {
		t.Errorf("sandbox label mismatch")
	}
	c := pod.Spec.Containers[0]
	if c.Image != "alpine:3.20" {
		t.Errorf("image: %s", c.Image)
	}
	if c.SecurityContext == nil ||
		c.SecurityContext.ReadOnlyRootFilesystem == nil ||
		!*c.SecurityContext.ReadOnlyRootFilesystem {
		t.Errorf("readOnlyRootFilesystem not enforced")
	}
	if c.SecurityContext.Capabilities == nil ||
		len(c.SecurityContext.Capabilities.Drop) == 0 ||
		string(c.SecurityContext.Capabilities.Drop[0]) != "ALL" {
		t.Errorf("capabilities.drop=ALL missing")
	}
	if pod.Spec.AutomountServiceAccountToken == nil ||
		*pod.Spec.AutomountServiceAccountToken {
		t.Errorf("automountServiceAccountToken should be false")
	}
}

func TestK8sList_FiltersByOwner(t *testing.T) {
	cs := fake.NewSimpleClientset()
	markRunning(t, cs)
	d := NewK8sWithClient(cs, nil, "biumind-sandbox")
	for _, owner := range []string{"alice", "alice", "bob"} {
		if _, err := d.Create(context.Background(),
			CreateInput{OwnerID: owner, Image: "alpine"}); err != nil {
			t.Fatalf("create %s: %v", owner, err)
		}
	}
	mine, err := d.List(context.Background(), "alice")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mine) != 2 {
		t.Errorf("alice should see 2, got %d", len(mine))
	}
	bob, _ := d.List(context.Background(), "bob")
	if len(bob) != 1 {
		t.Errorf("bob: %d", len(bob))
	}
	all, _ := d.List(context.Background(), "")
	if len(all) != 3 {
		t.Errorf("empty owner = all sandboxes: got %d", len(all))
	}
}

func TestK8sDestroy_RoundtripAndNotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	markRunning(t, cs)
	d := NewK8sWithClient(cs, nil, "biumind-sandbox")
	sb, _ := d.Create(context.Background(), CreateInput{OwnerID: "u", Image: "alpine"})

	if err := d.Destroy(context.Background(), sb.ID); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if err := d.Destroy(context.Background(), sb.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second destroy should ErrNotFound, got %v", err)
	}
	if _, err := d.Get(context.Background(), sb.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get-after-destroy should ErrNotFound, got %v", err)
	}
}

func TestK8sPauseSnapshotNotSupported(t *testing.T) {
	d := NewK8sWithClient(fake.NewSimpleClientset(), nil, "biumind-sandbox")
	if err := d.Pause(context.Background(), "x"); !errors.Is(err, ErrNotSupported) {
		t.Errorf("Pause: %v", err)
	}
	if err := d.Resume(context.Background(), "x"); !errors.Is(err, ErrNotSupported) {
		t.Errorf("Resume: %v", err)
	}
	if _, err := d.Snapshot(context.Background(), "x"); !errors.Is(err, ErrNotSupported) {
		t.Errorf("Snapshot: %v", err)
	}
}

func TestK8sCreateRejectsEmptyOwner(t *testing.T) {
	d := NewK8sWithClient(fake.NewSimpleClientset(), nil, "biumind-sandbox")
	if _, err := d.Create(context.Background(),
		CreateInput{Image: "alpine"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("want ErrInvalid for missing owner, got %v", err)
	}
}

func TestK8sCreate_RuntimeClassPropagated(t *testing.T) {
	cs := fake.NewSimpleClientset()
	markRunning(t, cs)
	d := NewK8sWithClient(cs, nil, "biumind-sandbox")
	d.RuntimeClass = "gvisor"
	sb, err := d.Create(context.Background(),
		CreateInput{OwnerID: "u", Image: "alpine"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pod, _ := cs.CoreV1().Pods("biumind-sandbox").Get(
		context.Background(), sb.ID, metav1.GetOptions{})
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "gvisor" {
		t.Errorf("runtimeClassName not propagated: %+v", pod.Spec.RuntimeClassName)
	}
}

func TestK8sWaitRunning_Timeout(t *testing.T) {
	// No reactor — pods stay in Phase="" forever; Create's wait must
	// budget out and surface a clear error rather than hang.
	cs := fake.NewSimpleClientset()
	d := NewK8sWithClient(cs, nil, "biumind-sandbox")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := d.Create(ctx, CreateInput{OwnerID: "u", Image: "alpine"}); err == nil {
		t.Errorf("expected wait timeout, got nil")
	}
}
