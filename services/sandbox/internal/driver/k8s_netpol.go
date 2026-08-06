// Per-Pod NetworkPolicy egress allowlists.
//
// The cluster manifest (cluster-side setup, not in this repo) installs
// a namespace-wide default-deny + a DNS allowance. That gives us the
// "default deny" half of the model. This file owns the "allow this Pod
// to reach those hosts on those ports" half, applied per sandbox at
// create time and torn down on destroy.
//
// Why per-Pod and not per-namespace? Different sandboxes have different
// trust profiles — a code-eval sandbox needs nothing; a webclip sandbox
// needs HTTPS to the open internet; a customer-integration sandbox
// needs ONE specific API endpoint. Encoding that in the cluster manifest
// is impossible, so the driver writes it at create time and deletes it
// at destroy time, paired with the Pod by a label selector.

package driver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// EgressRule is one parsed entry from CreateInput.EgressAllow.
//
// Wire format (string per entry):
//
//	"<host>:<port>"               — TCP allow to one host:port
//	"<host>:<port>/tcp"           — explicit TCP
//	"<host>:<port>/udp"           — UDP (DNS comes pre-allowed in 50-net.yaml)
//	"<cidr>:<port>"               — same, but the host is an IP/CIDR (no DNS lookup needed)
//
// Hostnames are resolved at NetworkPolicy create time so we can encode
// IP CIDRs (NetworkPolicy doesn't speak DNS). On resolve failure we
// drop the entry with a warn — better to deny by default than allow
// the wrong IP.
type EgressRule struct {
	CIDRs    []string                  // resolved /32 (or original CIDR) for ipBlock
	Port     int32
	Protocol corev1.Protocol
}

// parseEgressAllow turns the user-facing strings into EgressRule structs.
// Returns the rules + a slice of (entry, error) for the caller to log.
func parseEgressAllow(entries []string, resolver func(string) ([]net.IP, error)) (
	[]EgressRule, []error,
) {
	if resolver == nil {
		resolver = net.LookupIP
	}
	var (
		out  []EgressRule
		errs []error
	)
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// Split off optional /tcp /udp suffix. Only strip an explicit
		// proto tag — bare "/" inside CIDR notation (10.0.0.0/24:5432)
		// must NOT be treated as a proto separator.
		proto := corev1.ProtocolTCP
		switch {
		case strings.HasSuffix(strings.ToLower(raw), "/tcp"):
			raw = raw[:len(raw)-4]
		case strings.HasSuffix(strings.ToLower(raw), "/udp"):
			proto = corev1.ProtocolUDP
			raw = raw[:len(raw)-4]
		}
		// host:port or cidr:port — last colon is the separator (handles IPv6
		// with brackets, but we don't try to parse [::1]:443 right now).
		colon := strings.LastIndex(raw, ":")
		if colon <= 0 {
			errs = append(errs, fmt.Errorf("egress %q: missing :port", raw))
			continue
		}
		host, portStr := raw[:colon], raw[colon+1:]
		portInt, err := strconv.Atoi(portStr)
		if err != nil || portInt <= 0 || portInt > 65535 {
			errs = append(errs, fmt.Errorf("egress %q: bad port %q", raw, portStr))
			continue
		}

		var cidrs []string
		if _, _, err := net.ParseCIDR(host); err == nil {
			cidrs = []string{host}
		} else if ip := net.ParseIP(host); ip != nil {
			cidrs = []string{ipToCIDR(ip)}
		} else {
			ips, lookupErr := resolver(host)
			if lookupErr != nil || len(ips) == 0 {
				errs = append(errs,
					fmt.Errorf("egress %q: dns lookup failed: %v", raw, lookupErr))
				continue
			}
			for _, ip := range ips {
				cidrs = append(cidrs, ipToCIDR(ip))
			}
		}
		out = append(out, EgressRule{
			CIDRs:    cidrs,
			Port:     int32(portInt), //nolint:gosec — bounds checked above
			Protocol: proto,
		})
	}
	return out, errs
}

func ipToCIDR(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String() + "/32"
	}
	return ip.String() + "/128"
}

// netpolName is the deterministic NetworkPolicy name for a sandbox id.
// Same as the Pod name to keep `kubectl get netpol sbx-…` symmetric.
func netpolName(sandboxID string) string { return sandboxID }

// applyEgressNetworkPolicy creates a per-Pod NetworkPolicy that allows
// the configured egress rules. Returns nil when there are no rules
// (the namespace default-deny stays in effect → no egress).
func (k *K8s) applyEgressNetworkPolicy(
	ctx context.Context, sandboxID string, rules []EgressRule,
) error {
	if len(rules) == 0 {
		return nil
	}
	pol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      netpolName(sandboxID),
			Namespace: k.Namespace,
			Labels: map[string]string{
				labelSandbox: sandboxID,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelSandbox: sandboxID,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeEgress,
			},
			Egress: egressRulesToK8s(rules),
		},
	}
	_, err := k.Client.NetworkingV1().NetworkPolicies(k.Namespace).
		Create(ctx, pol, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("k8s: create netpol: %w", err)
	}
	return nil
}

// deleteEgressNetworkPolicy removes the per-Pod NetworkPolicy on Destroy.
// IsNotFound is swallowed — caller may have skipped applyEgressNetworkPolicy
// (no rules) or the policy may already have been GC'd by namespace teardown.
func (k *K8s) deleteEgressNetworkPolicy(ctx context.Context, sandboxID string) error {
	err := k.Client.NetworkingV1().NetworkPolicies(k.Namespace).
		Delete(ctx, netpolName(sandboxID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func egressRulesToK8s(rules []EgressRule) []networkingv1.NetworkPolicyEgressRule {
	// Group by CIDR set so equal-host different-port rules collapse.
	out := make([]networkingv1.NetworkPolicyEgressRule, 0, len(rules))
	for _, r := range rules {
		port := intstr.FromInt32(r.Port)
		proto := r.Protocol
		toPeers := make([]networkingv1.NetworkPolicyPeer, 0, len(r.CIDRs))
		for _, c := range r.CIDRs {
			toPeers = append(toPeers, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: c},
			})
		}
		out = append(out, networkingv1.NetworkPolicyEgressRule{
			To: toPeers,
			Ports: []networkingv1.NetworkPolicyPort{{
				Protocol: &proto,
				Port:     &port,
			}},
		})
	}
	return out
}

// ErrEgressInvalid is returned when CreateInput.EgressAllow has entries
// the driver couldn't parse. Caller sees this aggregated, not per-entry.
var ErrEgressInvalid = errors.New("egress allow entries invalid")
