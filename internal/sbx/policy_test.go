package sbx

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

const policyJSON = `{"rules":[
 {"id":"default-ai-services","name":"default-ai-services","scope":"global","applies_to":"all","resource_type":"network","decision":"allow","origin":"local","layer":"local","status":"active","editable":true,"resources":["api.example.com:443"],"provenance":{"created_via":"added"}},
 {"id":"0b9f15ee","name":"45868513","scope":"global","applies_to":"all","resource_type":"network","decision":"allow","status":"active","resources":["*.example.com"]},
 {"id":"fcb1e0e2","name":"kit:am","scope":"sandbox:am","resource_type":"network","decision":"allow","status":"active","resources":["example.org"]}
]}`

func TestPolicyRulesReadsEveryRule(t *testing.T) {
	f := &Fake{Responses: map[string]Response{"policy ls --json": {Output: []byte(policyJSON)}}}
	got, err := PolicyRules(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	want := []Rule{
		{ID: "default-ai-services", Scope: "global", ResourceType: "network", Decision: "allow", Status: "active", Resources: []string{"api.example.com:443"}},
		{ID: "0b9f15ee", Scope: "global", ResourceType: "network", Decision: "allow", Status: "active", Resources: []string{"*.example.com"}},
		{ID: "fcb1e0e2", Scope: "sandbox:am", ResourceType: "network", Decision: "allow", Status: "active", Resources: []string{"example.org"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestPolicyRulesRefusesAnUnreadableListing(t *testing.T) {
	f := &Fake{Responses: map[string]Response{"policy ls --json": {Output: []byte(`not json`)}}}
	if _, err := PolicyRules(context.Background(), f); err == nil {
		t.Error("want an error")
	}
}

func TestSSHForwardingReadsBothSettings(t *testing.T) {
	f := &Fake{Responses: map[string]Response{"settings ls --json": {Output: []byte(`[
		{"key":"browser.enabled","value":true},
		{"key":"ssh.agentForwardingEnabled","value":false,"type":"bool"},
		{"key":"ssh.agentSocketPath","value":"/run/agent.sock","type":"string"}]`)}}}
	got, err := SSHForwarding(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Forwarding{Enabled: false, Socket: "/run/agent.sock"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSSHForwardingRefusesAMissingKey(t *testing.T) {
	f := &Fake{Responses: map[string]Response{"settings ls --json": {Output: []byte(`[{"key":"ssh.agentSocketPath","value":""}]`)}}}
	if _, err := SSHForwarding(context.Background(), f); err == nil {
		t.Error("want an error: without ssh.agentForwardingEnabled den cannot tell whether the VM gets the agent")
	}
}

func TestSSHForwardingPassesTheRunnerError(t *testing.T) {
	boom := errors.New("daemon down")
	f := &Fake{Default: Response{Err: boom}}
	if _, err := SSHForwarding(context.Background(), f); !errors.Is(err, boom) {
		t.Errorf("err %v, want %v", err, boom)
	}
}
