package driver

import (
	"context"
	"errors"
	"net"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// stubResolver returns deterministic IPs for hosts so the tests don't
// hit real DNS (and so they run in air-gapped CI).
func stubResolver(table map[string][]net.IP) func(string) ([]net.IP, error) {
	return func(host string) ([]net.IP, error) {
		ips, ok := table[host]
		if !ok {
			return nil, errors.New("host not in stub table")
		}
		return ips, nil
	}
}

func TestParseEgressAllow_HostPort(t *testing.T) {
	rules, errs := parseEgressAllow([]string{"api.example.com:443"},
		stubResolver(map[string][]net.IP{
			"api.example.com": {net.ParseIP("203.0.113.5")},
		}))
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if len(rules) != 1 {
		t.Fatalf("rules: %+v", rules)
	}
	if rules[0].Port != 443 || rules[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("rule: %+v", rules[0])
	}
	if rules[0].CIDRs[0] != "203.0.113.5/32" {
		t.Errorf("cidr: %+v", rules[0].CIDRs)
	}
}

func TestParseEgressAllow_CIDR(t *testing.T) {
	rules, errs := parseEgressAllow([]string{"10.0.0.0/24:5432"}, nil)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if len(rules) != 1 || rules[0].CIDRs[0] != "10.0.0.0/24" {
		t.Errorf("rule: %+v", rules[0])
	}
}

func TestParseEgressAllow_RawIP(t *testing.T) {
	rules, errs := parseEgressAllow([]string{"10.0.0.5:8080"}, nil)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if rules[0].CIDRs[0] != "10.0.0.5/32" {
		t.Errorf("cidr: %s", rules[0].CIDRs[0])
	}
}

func TestParseEgressAllow_UDPSuffix(t *testing.T) {
	rules, _ := parseEgressAllow([]string{"10.0.0.5:53/udp"}, nil)
	if len(rules) != 1 || rules[0].Protocol != corev1.ProtocolUDP {
		t.Errorf("expected UDP rule; got %+v", rules)
	}
}

func TestParseEgressAllow_RejectsBad(t *testing.T) {
	bad := []string{
		"missing-port",
		"host:notnum",
		"host:99999",
		"unresolvable.example.invalid:443",
		":443",
		"host:443/icmp",
	}
	rules, errs := parseEgressAllow(bad, stubResolver(map[string][]net.IP{}))
	if len(rules) != 0 {
		t.Errorf("expected zero rules; got %+v", rules)
	}
	if len(errs) != len(bad) {
		t.Errorf("want %d errs, got %d: %v", len(bad), len(errs), errs)
	}
}

func TestParseEgressAllow_DNSReturnsMultipleIPs(t *testing.T) {
	rules, _ := parseEgressAllow([]string{"multi.example.com:443"},
		stubResolver(map[string][]net.IP{
			"multi.example.com": {net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2")},
		}))
	if len(rules) != 1 || len(rules[0].CIDRs) != 2 {
		t.Fatalf("expected 1 rule with 2 cidrs; got %+v", rules)
	}
}

// ─── End-to-end via fake clientset ──────────────────────

func TestK8sCreate_AppliesNetworkPolicyForEgressAllow(t *testing.T) {
	cs := fake.NewSimpleClientset()
	markRunning(t, cs)
	d := NewK8sWithClient(cs, nil, "biumind-sandbox")

	sb, err := d.Create(context.Background(), CreateInput{
		OwnerID:     "u",
		Image:       "alpine",
		EgressAllow: []string{"10.0.0.5:443", "10.0.0.6:8080"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pol, err := cs.NetworkingV1().NetworkPolicies("biumind-sandbox").
		Get(context.Background(), sb.ID, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get netpol: %v", err)
	}
	if pol.Spec.PodSelector.MatchLabels[labelSandbox] != sb.ID {
		t.Errorf("netpol selector mismatch: %+v", pol.Spec.PodSelector)
	}
	if len(pol.Spec.Egress) != 2 {
		t.Errorf("expected 2 egress rules; got %d", len(pol.Spec.Egress))
	}
	if len(pol.Spec.PolicyTypes) != 1 ||
		pol.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("policyTypes: %+v", pol.Spec.PolicyTypes)
	}
}

func TestK8sCreate_NoNetworkPolicyWhenEgressEmpty(t *testing.T) {
	cs := fake.NewSimpleClientset()
	markRunning(t, cs)
	d := NewK8sWithClient(cs, nil, "biumind-sandbox")

	sb, err := d.Create(context.Background(), CreateInput{
		OwnerID: "u", Image: "alpine",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = cs.NetworkingV1().NetworkPolicies("biumind-sandbox").
		Get(context.Background(), sb.ID, metav1.GetOptions{})
	if err == nil {
		t.Errorf("expected NetworkPolicy not found when EgressAllow empty " +
			"(namespace default-deny does the work)")
	}
}

func TestK8sCreate_RejectsAllInvalidEgress(t *testing.T) {
	cs := fake.NewSimpleClientset()
	markRunning(t, cs)
	d := NewK8sWithClient(cs, nil, "biumind-sandbox")

	_, err := d.Create(context.Background(), CreateInput{
		OwnerID:     "u",
		Image:       "alpine",
		EgressAllow: []string{"garbage", "host:notnum"},
	})
	if !errors.Is(err, ErrEgressInvalid) {
		t.Errorf("expected ErrEgressInvalid; got %v", err)
	}
	pods, _ := cs.CoreV1().Pods("biumind-sandbox").
		List(context.Background(), metav1.ListOptions{})
	if len(pods.Items) != 0 {
		t.Errorf("no pod should have been created on rejected egress; got %d",
			len(pods.Items))
	}
}

func TestK8sDestroy_AlsoDeletesNetworkPolicy(t *testing.T) {
	cs := fake.NewSimpleClientset()
	markRunning(t, cs)
	d := NewK8sWithClient(cs, nil, "biumind-sandbox")

	sb, err := d.Create(context.Background(), CreateInput{
		OwnerID: "u", Image: "alpine",
		EgressAllow: []string{"10.0.0.1:443"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.Destroy(context.Background(), sb.ID); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	_, err = cs.NetworkingV1().NetworkPolicies("biumind-sandbox").
		Get(context.Background(), sb.ID, metav1.GetOptions{})
	if err == nil {
		t.Errorf("netpol should be deleted on destroy")
	}
}
