package main

import (
	"reflect"
	"testing"
)

func TestSplitRoleFlag(t *testing.T) {
	tests := []struct {
		in       []string
		wantArgs []string
		wantRole string
	}{
		{[]string{"add", "-role", "admin", "bob"}, []string{"add", "bob"}, "admin"},
		{[]string{"add", "bob", "--role=operator"}, []string{"add", "bob"}, "operator"},
		{[]string{"list"}, []string{"list"}, "viewer"},
		{[]string{"del", "--", "-role"}, []string{"del", "-role"}, "viewer"},
	}
	for _, tt := range tests {
		args, role, err := splitRoleFlag(tt.in, "viewer")
		if err != nil || !reflect.DeepEqual(args, tt.wantArgs) || role != tt.wantRole {
			t.Errorf("%v -> %v %q %v", tt.in, args, role, err)
		}
	}
	if _, _, err := splitRoleFlag([]string{"add", "bob", "-role"}, "viewer"); err == nil {
		t.Error("dangling -role accepted")
	}
}
