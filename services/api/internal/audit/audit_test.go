package audit

import "testing"

func TestEventValidation(t *testing.T) {
	good := []Event{
		{ActorType: ActorUser, ActorUserID: "u1", Action: "a.b", EntityType: "x", EntityID: "1"},
		{ActorType: ActorSystem, Action: "job.run", EntityType: "x", EntityID: "1"},
		{ActorType: ActorCLI, Action: "user.role_set", EntityType: "user", EntityID: "1"},
	}
	for i, e := range good {
		if err := e.Validate(); err != nil {
			t.Errorf("good %d rejected: %v", i, err)
		}
	}
	bad := []Event{
		{ActorType: ActorUser, Action: "a.b", EntityType: "x", EntityID: "1"},                       // user without id
		{ActorType: "robot", ActorUserID: "u", Action: "a.b", EntityType: "x", EntityID: "1"},       // unknown actor
		{ActorType: ActorSystem, Action: "ab", EntityType: "x", EntityID: "1"},                      // action too short
		{ActorType: ActorSystem, Action: string(make([]byte, 101)), EntityType: "x", EntityID: "1"}, // action too long
		{ActorType: ActorSystem, Action: "a.b", EntityID: "1"},                                      // no entity type
		{ActorType: ActorSystem, Action: "a.b", EntityType: "x"},                                    // no entity id
	}
	for i, e := range bad {
		if e.Validate() == nil {
			t.Errorf("bad %d accepted", i)
		}
	}
}

func TestSanitizeRemovesSecretsRecursivelyAndKeepsTheRest(t *testing.T) {
	in := map[string]any{
		"password": "x", "Reset_Token": "y", "api_key": "z", "ok": "yes", "from": "draft",
		"nested": map[string]any{"authorization": "Bearer abc", "keep": 1},
	}
	out := Sanitize(in)
	for _, k := range []string{"password", "Reset_Token", "api_key"} {
		if _, ok := out[k]; ok {
			t.Errorf("%s survived", k)
		}
	}
	if out["ok"] != "yes" || out["from"] != "draft" {
		t.Fatalf("lost safe keys: %v", out)
	}
	n := out["nested"].(map[string]any)
	if _, ok := n["authorization"]; ok || n["keep"] != 1 {
		t.Fatalf("nested not sanitized: %v", n)
	}
	if _, ok := in["password"]; !ok {
		t.Fatal("Sanitize must not mutate its input")
	}
	if got := Sanitize(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil map: %v", got)
	}
}

func TestFilterLimit(t *testing.T) {
	if (Filter{}).EffectiveLimit() != 50 || (Filter{Limit: 9999}).EffectiveLimit() != 200 || (Filter{Limit: 7}).EffectiveLimit() != 7 {
		t.Fatal("limit clamping wrong")
	}
}
