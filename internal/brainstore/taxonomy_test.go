package brainstore

import "testing"

func TestCapabilityFromCLISubcommand_WholeCLI(t *testing.T) {
	cases := map[string]string{
		"graph": CapabilityGraph, "brain": CapabilityBrain, "judge": CapabilityJudge,
		"session": CapabilitySession, "checkpoint": CapabilitySession, "cp": CapabilitySession,
		"trail": CapabilityTrail, "review": CapabilityTrail,
		"enable": CapabilitySetup, "doctor": CapabilitySetup, "auth": CapabilitySetup,
		"tokens": CapabilityInsight, "blame": CapabilityInsight, "why": CapabilityInsight,
		"totally-unknown": CapabilityOther,
	}
	for sub, want := range cases {
		if got := capabilityFromCLISubcommand(sub); got != want {
			t.Errorf("entire %s -> %s, want %s", sub, got, want)
		}
	}
}
