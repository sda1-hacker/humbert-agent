package services

import (
	"reflect"
	"testing"
)

func TestFilterRemovedBuiltinToolsDropsLegacyDelegationSelection(t *testing.T) {
	input := []string{"read_file", "cancel_delegation", "delegate_task", "run_agent", "delegation_status"}
	want := []string{"read_file", "run_agent"}
	if got := filterRemovedBuiltinTools(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("filterRemovedBuiltinTools() = %#v, want %#v", got, want)
	}
}
