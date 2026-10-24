package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
)

func TestDecision_ZeroValueIsFailClosed(t *testing.T) {
	if agent.Decision(0) != agent.DecisionUnknown {
		t.Fatalf("zero Decision = %v, want DecisionUnknown", agent.Decision(0))
	}
	if got := agent.DecisionUnknown.String(); got != "unknown" {
		t.Errorf("DecisionUnknown.String() = %q", got)
	}
}

func TestPolicyChain_BeforeToolUsesExplicitRestrictiveness(t *testing.T) {
	policyFor := func(decision agent.Decision) agent.Policy {
		return agent.Hooks{
			OnBeforeTool: func(context.Context, *agent.ToolRequest) (agent.Decision, error) {
				return decision, nil
			},
		}
	}

	tests := []struct {
		name  string
		chain agent.PolicyChain
		want  agent.Decision
	}{
		{
			name:  "all allow",
			chain: agent.PolicyChain{policyFor(agent.Allow), policyFor(agent.Allow)},
			want:  agent.Allow,
		},
		{
			name:  "deny wins over allow",
			chain: agent.PolicyChain{policyFor(agent.Allow), policyFor(agent.Deny)},
			want:  agent.Deny,
		},
		{
			name:  "ask wins over deny",
			chain: agent.PolicyChain{policyFor(agent.Deny), policyFor(agent.Ask)},
			want:  agent.Ask,
		},
		{
			name:  "terminate wins",
			chain: agent.PolicyChain{policyFor(agent.Terminate), policyFor(agent.Ask), policyFor(agent.Deny)},
			want:  agent.Terminate,
		},
		{
			name:  "unknown fails closed",
			chain: agent.PolicyChain{policyFor(agent.DecisionUnknown), policyFor(agent.Allow)},
			want:  agent.DecisionUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.chain.BeforeTool(context.Background(), &agent.ToolRequest{})
			if err != nil {
				t.Fatalf("BeforeTool: %v", err)
			}
			if got != tc.want {
				t.Errorf("decision = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPolicyChain_ErrorDenies(t *testing.T) {
	boom := errors.New("boom")
	chain := agent.PolicyChain{agent.Hooks{
		OnBeforeTool: func(context.Context, *agent.ToolRequest) (agent.Decision, error) {
			return agent.Allow, boom
		},
	}}

	decision, err := chain.BeforeTool(context.Background(), &agent.ToolRequest{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if decision != agent.Deny {
		t.Errorf("decision = %v, want Deny", decision)
	}
}
