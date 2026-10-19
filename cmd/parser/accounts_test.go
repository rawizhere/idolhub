package main

import (
	"testing"

	"idolhub/internal/config"
)

func TestAccountPatchApply(t *testing.T) {
	str := func(s string) *string { return &s }
	yes := true
	filters := []string{" cat ", "Cat", "", "dog"}
	acc := config.Account{Platform: "twitter", Username: "a", Filters: []string{}}

	changed, err := accountPatch{Paused: &yes}.apply(&acc)
	if err != nil || changed || !acc.Paused {
		t.Errorf("pause: changed=%v err=%v acc=%+v; pausing must not reset the watermark", changed, err, acc)
	}

	changed, err = accountPatch{Filters: &filters}.apply(&acc)
	if err != nil || !changed || len(acc.Filters) != 2 || acc.Filters[0] != "cat" {
		t.Errorf("filters: changed=%v err=%v filters=%q", changed, err, acc.Filters)
	}

	if _, err := (accountPatch{Since: str("2099-01-01")}).apply(&acc); err == nil {
		t.Error("future since accepted")
	}
	if _, err := (accountPatch{Since: str("31.01.2024")}).apply(&acc); err == nil {
		t.Error("malformed since accepted")
	}
	if _, err := (accountPatch{Cookies: str("not a cookie file")}).apply(&acc); err == nil {
		t.Error("garbage cookies accepted")
	}
	changed, err = accountPatch{Cookies: str(`[{"domain":".x.com","name":"auth_token","value":"t","path":"/","expirationDate":1900000000}]`)}.apply(&acc)
	if err != nil || changed || acc.Cookies == "" {
		t.Errorf("json cookies: changed=%v err=%v", changed, err)
	}
}

func TestCleanUsername(t *testing.T) {
	for in, want := range map[string]string{"@alice/": "alice", " bob ": "bob"} {
		if got, err := cleanUsername(in); err != nil || got != want {
			t.Errorf("cleanUsername(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "../x", "a b", "a/b"} {
		if _, err := cleanUsername(bad); err == nil {
			t.Errorf("cleanUsername(%q) accepted", bad)
		}
	}
}
