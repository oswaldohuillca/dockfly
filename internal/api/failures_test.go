package api

import (
	"strconv"
	"testing"
)

// The lockout is per account and is lifted once the credentials are correct.
func TestFailures(t *testing.T) {
	var f failures[string]
	for range loginMaxFailures {
		if f.blocked(loginKey("Ana@test.com"), loginMaxFailures) {
			t.Fatal("blocked too early")
		}
		f.fail(loginKey("ana@test.com "))
	}
	if !f.blocked(loginKey("ANA@test.com"), loginMaxFailures) {
		t.Fatal("the account must be locked out after too many failures")
	}
	if f.blocked("other@test.com", loginMaxFailures) {
		t.Fatal("the lockout must not affect other accounts")
	}
	f.reset("ana@test.com")
	if f.blocked("ana@test.com", loginMaxFailures) {
		t.Fatal("reset must lift the lockout")
	}
}

// Filling the map with made-up emails does not disable an account's lockout.
func TestFailuresFullMapStillCounts(t *testing.T) {
	var f failures[string]
	for i := range failMaxTracked {
		f.fail(strconv.Itoa(i) + "@spam.test")
	}
	for range loginMaxFailures {
		f.fail("admin@test.com")
	}
	if !f.blocked("admin@test.com", loginMaxFailures) {
		t.Fatal("the real account must still get locked out with a full map")
	}
}
