package pennybase

import "testing"

func TestFieldType_String(t *testing.T) {
	tests := []struct {
		name string
		ft   FieldType
		want string
	}{
		{"unknown", Unknown, "Unknown"},
		{"number", Number, "Number"},
		{"text", Text, "Text"},
		{"list", List, "List"},
		{"large", 123, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ft.String(); got != tt.want {
				t.Errorf("FieldType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}
