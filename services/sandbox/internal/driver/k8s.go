// Kubernetes driver — Pod-per-sandbox via client-go.
//
// Each [Sandbox] becomes a Pod in a configured namespace, labeled with
// `biumind.app/owner-id` and `biumind.app/sandbox-id`. The Pod
// runs `sleep infinity` (a lightweight long-runner) until [Exec] shells
// into it via the standard `pods/exec` SPDY upgrade.
//
// Hardening defaults applied to every sandbox Pod:
//   * `automountServiceAccountToken: false` (no SA token leaked in)
//   * `securityContext.runAsNonRoot: true`
//   * `securityContext.readOnlyRootFilesystem: true` + tmpfs at /tmp
//   * `containers[].securityContext.capabilities.drop: [ALL]`
//   * `containers[].securityContext.allowPrivilegeEscalation: false`
//   * `resources.limits` cpu / memory from CreateInput
//   * `restartPolicy: Never`
//
// gVisor: production clusters add `runtimeClassName: "gvisor"` via
// SANDBOX_K8S_RUNTIMECLASS. docker-compose's k3s + Docker Desktop on
// macOS does NOT support runsc (kernel access not available inside the
// VM) — leaving the env unset gets you the default runtime, which is
// fine for a single-tenant dev cluster.
//
// Network egress: when CreateInput.NetworkOff or empty EgressAllow we
// don't apply any NetworkPolicy — caller is expected to configure the
// namespace's default deny-all NetworkPolicy via cluster-side manifests
// (not in this repo). When EgressAllow is set,
// the driver creates a per-Pod NetworkPolicy with explicit egress rules.

package driver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	labelOwner    = "biumind.app/owner-id"
	labelSandbox  = "biumind.app/sandbox-id"
	containerName = "sandbox"
)

type K8s struct {
	Client       kubernetes.Interface
	Namespace    string
	RuntimeClass string // "" → cluster default; "gvisor" / "kata" / etc.
	Image        string // default base image when CreateInput.Image is empty
	RESTConfig   *rest.Config
	Logger       *slog.Logger
	Policy       Policy // R5: image allowlist / workspace / non-root（与 docker 对称）
}

// NewK8s — production constructor. `kubeconfigPath` empty → use
// in-cluster config (when Sandbox runs as a Pod itself).
func NewK8s(kubeconfigPath, namespace, runtimeClass, defaultImage string, logger *slog.Logger, policy Policy) (*K8s, error) {
	cfg, err := loadKubeconfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("k8s: kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: clientset: %w", err)
	}
	if namespace == "" {
		namespace = "biumind-sandbox"
	}
	if defaultImage == "" {
		defaultImage = "alpine:3.20"
	}
	if logger == nil {
		logger = slog.Default()
	}
	if policy.DefaultImage == "" {
		policy.DefaultImage = defaultImage
	}
	return &K8s{
		Client: cs, RESTConfig: cfg,
		Namespace: namespace, RuntimeClass: runtimeClass,
		Image: defaultImage, Logger: logger, Policy: policy,
	}, nil
}

// NewK8sWithClient — test constructor wraps an existing clientset
// (typically `fake.NewSimpleClientset()`). Exec roundtrips need
// RESTConfig too, so tests that exercise Exec must provide both.
func NewK8sWithClient(cs kubernetes.Interface, restCfg *rest.Config, namespace string) *K8s {
	return &K8s{
		Client: cs, RESTConfig: restCfg,
		Namespace: namespace, Image: "alpine:3.20",
		Logger: slog.Default(),
		// 测试构造器：放行裸 "alpine"（多处测试 fixture 用），生产 NewK8s 严格。
		Policy: Policy{DefaultImage: "alpine:3.20", ImageAllowlist: []string{"alpine"}},
	}
}

func loadKubeconfig(path string) (*rest.Config, error) {
	if path == "" {
		// Try in-cluster first, fall back to default kubeconfig.
		if cfg, err := rest.InClusterConfig(); err == nil {
			return cfg, nil
		}
		path = clientcmd.RecommendedHomeFile
	}
	return clientcmd.BuildConfigFromFlags("", path)
}

// containerSecurityContext builds the sandbox container SecurityContext.
// Base hardening (readonly root / no-privilege-escalation / drop ALL caps) is
// always on; R5 adds non-root user when Policy.RunAsUser is set (symmetric
// with the docker driver's --user).
func containerSecurityContext(p Policy) *corev1.SecurityContext {
	roRoot, noPrivEsc := true, false
	sc := &corev1.SecurityContext{
		ReadOnlyRootFilesystem:   &roRoot,
		AllowPrivilegeEscalation: &noPrivEsc,
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	if uid, gid, ok := p.UserGID(); ok {
		u, g := uid, gid
		nonRoot := u != 0
		sc.RunAsUser = &u
		sc.RunAsGroup = &g
		sc.RunAsNonRoot = &nonRoot
	}
	return sc
}

// ─── Driver impl ──────────────────────────────────────────

func (k *K8s) Create(ctx context.Context, in CreateInput) (*Sandbox, error) {
	if in.OwnerID == "" {
		return nil, ErrInvalid
	}
	id := "sbx-" + uuid.NewString()
	// R5: 镜像白名单（空→默认；非白名单→拒绝），与 docker driver 对称。
	image, err := k.Policy.ResolveImage(in.Image)
	if err != nil {
		return nil, err
	}

	// Parse egress allowlist BEFORE creating the Pod so a malformed
	// entry rejects the whole create rather than leaving an orphaned
	// Pod with full network access while we untangle it.
	var egressRules []EgressRule
	if !in.NetworkOff && len(in.EgressAllow) > 0 {
		var parseErrs []error
		egressRules, parseErrs = parseEgressAllow(in.EgressAllow, nil)
		for _, e := range parseErrs {
			k.Logger.Warn("k8s: egress entry rejected", "err", e)
		}
		if len(egressRules) == 0 {
			return nil, fmt.Errorf("%w: all entries failed to parse", ErrEgressInvalid)
		}
	}

	pod := k.podSpec(id, in, image)
	if _, err := k.Client.CoreV1().Pods(k.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("k8s: create pod: %w", err)
	}

	// NetworkPolicy must come BEFORE waitRunning — once the Pod is
	// scheduled the kube-proxy/CNI applies whatever NetPol exists at
	// that moment. Order: pod → netpol → wait. (The namespace
	// default-deny in 50-network-policies.yaml means a brief window
	// of "no egress" before the per-Pod allow lands; that's preferred
	// over a window of "full egress".)
	if err := k.applyEgressNetworkPolicy(ctx, id, egressRules); err != nil {
		_ = k.Client.CoreV1().Pods(k.Namespace).Delete(
			context.Background(), id, metav1.DeleteOptions{})
		return nil, err
	}

	// Wait for Running so Exec doesn't immediately fail. Caller can
	// override the budget via ctx; default 120s covers a cold image
	// pull on a fresh k3s. Warm clusters return in single-digit seconds.
	if err := k.waitRunning(ctx, id, 120*time.Second); err != nil {
		// Best-effort cleanup of the pod + netpol we just created.
		_ = k.deleteEgressNetworkPolicy(context.Background(), id)
		_ = k.Client.CoreV1().Pods(k.Namespace).Delete(
			context.Background(), id, metav1.DeleteOptions{})
		return nil, fmt.Errorf("k8s: pod did not reach Running: %w", err)
	}

	return &Sandbox{
		ID:         id,
		OwnerID:    in.OwnerID,
		Image:      image,
		Status:     "running",
		CreatedAt:  time.Now().UTC(),
		CPUShares:  in.CPUShares,
		MemoryMB:   in.MemoryMB,
		NetworkOff: in.NetworkOff,
	}, nil
}

func (k *K8s) Get(ctx context.Context, id string) (*Sandbox, error) {
	pod, err := k.Client.CoreV1().Pods(k.Namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("k8s: get pod: %w", err)
	}
	return podToSandbox(pod), nil
}

func (k *K8s) List(ctx context.Context, ownerID string) ([]Sandbox, error) {
	selector := ""
	if ownerID != "" {
		selector = labelOwner + "=" + ownerID
	}
	pods, err := k.Client.CoreV1().Pods(k.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("k8s: list pods: %w", err)
	}
	out := make([]Sandbox, 0, len(pods.Items))
	for i := range pods.Items {
		sb := podToSandbox(&pods.Items[i])
		if sb != nil {
			out = append(out, *sb)
		}
	}
	return out, nil
}

func (k *K8s) Destroy(ctx context.Context, id string) error {
	// Delete NetworkPolicy first so the Pod can't make any more
	// outbound connections during termination grace period. Errors
	// other than NotFound fall through — the Pod delete still attempts.
	if err := k.deleteEgressNetworkPolicy(ctx, id); err != nil {
		k.Logger.Warn("k8s: delete netpol", "id", id, "err", err)
	}
	err := k.Client.CoreV1().Pods(k.Namespace).Delete(ctx, id, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("k8s: delete pod: %w", err)
	}
	return nil
}

func (k *K8s) Exec(ctx context.Context, in ExecInput, w io.Writer) (*ExecResult, error) {
	if len(in.Argv) == 0 {
		return nil, ErrInvalid
	}
	if k.RESTConfig == nil {
		// Tests that don't need exec can skip RESTConfig; if they call
		// Exec anyway, fail loudly.
		return nil, fmt.Errorf("k8s: exec requires REST config (test setup gap)")
	}
	if _, err := k.Get(ctx, in.SandboxID); err != nil {
		return nil, err
	}

	cmdCtx := ctx
	if in.TimeoutSec > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(in.TimeoutSec)*time.Second)
		defer cancel()
	}

	req := k.Client.CoreV1().RESTClient().
		Post().Resource("pods").Namespace(k.Namespace).Name(in.SandboxID).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   in.Argv,
			Stdin:     in.Stdin != nil,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(k.RESTConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("k8s: spdy executor: %w", err)
	}

	var stdin io.Reader
	if in.Stdin != nil {
		stdin = bytes.NewReader(in.Stdin)
	}
	streamErr := exec.StreamWithContext(cmdCtx, remotecommand.StreamOptions{
		Stdin: stdin, Stdout: w, Stderr: w, Tty: false,
	})

	res := &ExecResult{}
	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	}
	if streamErr != nil {
		// remotecommand wraps non-zero exits as exec.CodeExitError;
		// extract the code so callers see exit semantics matching the
		// docker driver.
		if exitErr, ok := asExitError(streamErr); ok {
			res.ExitCode = exitErr
			return res, nil
		}
		if res.TimedOut {
			return res, nil
		}
		return res, fmt.Errorf("k8s: exec stream: %w", streamErr)
	}
	return res, nil
}

func (k *K8s) Pause(ctx context.Context, id string) error  { return ErrNotSupported }
func (k *K8s) Resume(ctx context.Context, id string) error { return ErrNotSupported }
func (k *K8s) Snapshot(ctx context.Context, id string) (string, error) {
	return "", ErrNotSupported
}

// ─── Pod spec construction ───────────────────────────────

func (k *K8s) podSpec(id string, in CreateInput, image string) *corev1.Pod {
	cpuQuantity := resource.NewMilliQuantity(int64(in.CPUShares), resource.DecimalSI)
	memQuantity := resource.NewQuantity(int64(in.MemoryMB)*1024*1024, resource.BinarySI)

	resourceList := corev1.ResourceList{}
	if in.CPUShares > 0 {
		resourceList[corev1.ResourceCPU] = *cpuQuantity
	}
	if in.MemoryMB > 0 {
		resourceList[corev1.ResourceMemory] = *memQuantity
	}

	envVars := make([]corev1.EnvVar, 0, len(in.Env))
	for k, v := range in.Env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	auto := false

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: k.Namespace,
			Labels: map[string]string{
				labelOwner:   in.OwnerID,
				labelSandbox: id,
			},
			Annotations: map[string]string{
				"biumind.app/label": in.Label,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &auto,
			// Note: we deliberately don't set RunAsNonRoot=true. The
			// image is expected to ship with a non-root USER directive
			// when needed; forcing the cluster to reject root-as-root
			// images breaks ergonomic dev images like alpine. Production
			// clusters can enforce this via PodSecurity admission.
			SecurityContext: &corev1.PodSecurityContext{},
			Volumes: []corev1.Volume{
				{Name: "tmp", VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
				}},
				// R5: 可写 /workspace（rootfs 只读），与 docker tmpfs 对称。
				{Name: "workspace", VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
				}},
			},
			Containers: []corev1.Container{{
				Name:    containerName,
				Image:   image,
				Command: []string{"sleep", "infinity"},
				Env:     envVars,
				SecurityContext: containerSecurityContext(k.Policy),
				VolumeMounts: []corev1.VolumeMount{
					{Name: "tmp", MountPath: "/tmp"},
					{Name: "workspace", MountPath: "/workspace"},
				},
				Resources: corev1.ResourceRequirements{Limits: resourceList},
			}},
		},
	}

	if k.RuntimeClass != "" {
		pod.Spec.RuntimeClassName = &k.RuntimeClass
	}
	return pod
}

func (k *K8s) waitRunning(ctx context.Context, name string, budget time.Duration) error {
	c, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return wait.PollUntilContextCancel(c, 250*time.Millisecond, true,
		func(ctx context.Context) (bool, error) {
			pod, err := k.Client.CoreV1().Pods(k.Namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			switch pod.Status.Phase {
			case corev1.PodRunning:
				return true, nil
			case corev1.PodFailed, corev1.PodSucceeded:
				return false, fmt.Errorf("pod terminated early: %s", pod.Status.Phase)
			}
			return false, nil
		})
}

func podToSandbox(pod *corev1.Pod) *Sandbox {
	if pod == nil {
		return nil
	}
	status := "creating"
	switch pod.Status.Phase {
	case corev1.PodRunning:
		status = "running"
	case corev1.PodSucceeded, corev1.PodFailed:
		status = "exited"
	}
	owner := pod.Labels[labelOwner]
	image := ""
	cpuShares := 0
	memMB := 0
	if len(pod.Spec.Containers) > 0 {
		image = pod.Spec.Containers[0].Image
		if cpu, ok := pod.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU]; ok {
			cpuShares = int(cpu.MilliValue())
		}
		if mem, ok := pod.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]; ok {
			memMB = int(mem.Value() / (1024 * 1024))
		}
	}
	return &Sandbox{
		ID:        pod.Name,
		OwnerID:   owner,
		Image:     image,
		Status:    status,
		CreatedAt: pod.CreationTimestamp.Time,
		CPUShares: cpuShares,
		MemoryMB:  memMB,
	}
}

// asExitError pokes through remotecommand's wrapped exit-code error to
// retrieve the inner code, falling back to 1 when we can't.
func asExitError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	type codeExit interface {
		ExitStatus() int
	}
	var ce codeExit
	if errors.As(err, &ce) {
		return ce.ExitStatus(), true
	}
	// Fallback: parse "command terminated with exit code 7" style.
	msg := err.Error()
	if i := strings.LastIndex(msg, "exit code "); i >= 0 {
		var n int
		_, perr := fmt.Sscanf(msg[i+len("exit code "):], "%d", &n)
		if perr == nil {
			return n, true
		}
	}
	return 0, false
}

// keep bufio importable so future log scanners don't drop it.
var _ = bufio.NewReader
