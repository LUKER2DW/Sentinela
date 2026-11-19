package api

import (
	"fmt"
	"testing"
)

// A stranger who knows a username must not be able to lock the owner out of
// remote login by spending the account's attempts: the count is per account
// and per source, and only failures count.
func TestAccountLockIsPerSource(t *testing.T) {
	_, srv := newTestServer(t)
	for i := 0; i < 10; i++ {
		srv.acctFailed(" Admin ", "203.0.113.9")
	}
	if !srv.acctLocked("admin", "203.0.113.9") {
		t.Fatal("10 failures from one source must lock that source out of the account")
	}
	if srv.acctLocked("admin", "198.51.100.4") {
		t.Fatal("another source must not be locked by a stranger's failures")
	}
	if srv.acctLocked("someone-else", "203.0.113.9") {
		t.Fatal("the lock is per account")
	}
}

// A guess spread over many sources still hits the all-sources backstop.
func TestAccountLockBackstopForSpreadGuessing(t *testing.T) {
	_, srv := newTestServer(t)
	for i := 0; i < 300; i++ {
		srv.acctFailed("victim", fmt.Sprintf("2001:db8:%x::/64", i))
	}
	if !srv.acctLocked("victim", "192.0.2.77") {
		t.Fatal("300 failures from many sources must lock the account for everyone")
	}
}
