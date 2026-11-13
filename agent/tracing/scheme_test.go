package tracing

import "testing"

func TestSchemeKeyOperationName(t *testing.T) {
	cases := []struct {
		name   string
		scheme AttributeScheme
		want   string
	}{
		{"nil scheme emits default", nil, AttrGenAIOperationName},
		{"empty scheme emits default", AttributeScheme{}, AttrGenAIOperationName},
		{"current role key", AttributeScheme{RoleOperationName: "op"}, "op"},
		{"current role disabled", AttributeScheme{RoleOperationName: ""}, ""},
		{"legacy empty-role key", AttributeScheme{"": "op"}, "op"},
		{"current key wins over legacy", AttributeScheme{RoleOperationName: "new", "": "old"}, "new"},
	}
	for _, tc := range cases {
		if got := tc.scheme.Key(RoleOperationName); got != tc.want {
			t.Errorf("%s: Key = %q, want %q", tc.name, got, tc.want)
		}
	}
}
