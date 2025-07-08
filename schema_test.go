package pennybase

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"
)

const testID = "test0001"

func TestFieldSchema(t *testing.T) {
	tests := []struct {
		name     string
		field    FieldSchema
		value    Value
		expected bool
	}{
		// Number validation
		{
			name:     "valid number within range",
			field:    FieldSchema{Type: Number, Min: 5, Max: 10},
			value:    numberValue(7),
			expected: true,
		},
		{
			name:     "number at min boundary",
			field:    FieldSchema{Type: Number, Min: 5, Max: 10},
			value:    numberValue(5),
			expected: true,
		},
		{
			name:     "number at max boundary",
			field:    FieldSchema{Type: Number, Min: 5, Max: 10},
			value:    numberValue(10),
			expected: true,
		},
		{
			name:     "number below min",
			field:    FieldSchema{Type: Number, Min: 5, Max: 10},
			value:    numberValue(4.9),
			expected: false,
		},
		{
			name:     "number above max",
			field:    FieldSchema{Type: Number, Min: 5, Max: 10},
			value:    numberValue(10.1),
			expected: false,
		},
		{
			name:     "invalid number type",
			field:    FieldSchema{Type: Number},
			value:    textValue("not a number"),
			expected: false,
		},

		// Text validation
		{
			name:     "text matches regex",
			field:    FieldSchema{Type: Text, Regex: "^[a-z]+$"},
			value:    textValue("lowercase"),
			expected: true,
		},
		{
			name:     "text doesn't match regex",
			field:    FieldSchema{Type: Text, Regex: "^[a-z]+$"},
			value:    textValue("Uppercase"),
			expected: false,
		},
		{
			name:     "empty text with regex",
			field:    FieldSchema{Type: Text, Regex: "^.*$"},
			value:    textValue(""),
			expected: true,
		},

		// List validation
		{
			name:     "valid string list",
			field:    FieldSchema{Type: List},
			value:    listValue("a", "b"),
			expected: true,
		},
		{
			name:     "empty list",
			field:    FieldSchema{Type: List},
			value:    listValue(),
			expected: true,
		},
		{
			name:     "invalid list type",
			field:    FieldSchema{Type: List},
			value:    textValue("not a list"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expected != tt.field.Validate(tt.value) {
				t.Errorf("expected %v, got %v", tt.expected, tt.field.Validate(tt.value))
			}
		})
	}
}

func TestSchemaRecordConversion(t *testing.T) {
	testSchema := Schema{
		{Field: "_id", Type: Text, Regex: "^[A-Za-z0-9]+$"},
		{Field: "_v", Type: Number, Min: 1},
		{Field: "name", Type: Text, Regex: "^[A-Z][a-z]*$"},
		{Field: "age", Type: Number, Min: 0, Max: 150},
		{Field: "tags", Type: List},
	}

	tests := []struct {
		name        string
		resource    Resource
		expectedRec Record
		expectErr   bool
	}{
		{
			name: "valid complete resource",
			resource: Resource{
				"_id":  textValue(testID),
				"_v":   numberValue(1),
				"name": textValue("John"),
				"age":  numberValue(30),
				"tags": listValue("admin", "user"),
			},
			expectedRec: Record{
				testID,
				"1",
				"John",
				"30",
				"admin,user",
			},
			expectErr: false,
		},
		{
			name: "missing optional fields",
			resource: Resource{
				"_id":  textValue(testID),
				"_v":   numberValue(1),
				"name": textValue("John"),
			},
			expectedRec: Record{
				testID,
				"1",
				"John",
				"0",
				"",
			},
			expectErr: false,
		},
		{
			name: "invalid _id format",
			resource: Resource{
				"_id": textValue("?"),
				"_v":  numberValue(1),
			},
			expectErr: true,
		},
		{
			name: "invalid name regex",
			resource: Resource{
				"_id":  textValue(testID),
				"_v":   numberValue(1),
				"name": textValue("john"), // lowercase
				"age":  numberValue(30),
			},
			expectErr: true,
		},
		{
			name: "age out of range",
			resource: Resource{
				"_id": textValue(testID),
				"_v":  numberValue(1),
				"age": numberValue(200),
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := testSchema.Record(tt.resource)
			if gotErr := (err != nil); gotErr != tt.expectErr {
				t.Errorf("expected error %v, got %v", tt.expectErr, err)
			} else if !slices.Equal(rec, tt.expectedRec) {
				t.Errorf("expected %v, got %v", tt.expectedRec, rec)
			}
		})
	}
}

func TestSchemaResourceConversion(t *testing.T) {
	testSchema := Schema{
		{Field: "_id", Type: Text, Regex: "^[A-Za-z0-9]+$"},
		{Field: "_v", Type: Number, Min: 1},
		{Field: "name", Type: Text},
		{Field: "age", Type: Number},
		{Field: "tags", Type: List},
	}

	tests := []struct {
		name        string
		record      Record
		expectedRes Resource
		expectErr   bool
	}{
		{
			name:   "valid complete record",
			record: Record{testID, "2", "Alice", "25", "staff,manager"},
			expectedRes: Resource{
				"_id":  textValue(testID),
				"_v":   numberValue(2),
				"name": textValue("Alice"),
				"age":  numberValue(25),
				"tags": listValue("staff", "manager"),
			},
			expectErr: false,
		},
		{
			name:      "invalid record length",
			record:    Record{"ID", "1", "extra"},
			expectErr: true,
		},
		{
			name:      "invalid version format",
			record:    Record{testID, "invalid", "Alice", "25"},
			expectErr: true,
		},
		{
			name:      "invalid number format",
			record:    Record{testID, "1", "Alice", "notanumber"},
			expectErr: true,
		},
		{
			name:   "empty list field",
			record: Record{testID, "1", "Bob", "40", ""},
			expectedRes: Resource{
				"_id":  textValue(testID),
				"_v":   numberValue(1),
				"name": textValue("Bob"),
				"age":  numberValue(40),
				"tags": listValue(),
			},
			expectErr: false,
		},
		{
			name:   "single list item",
			record: Record{testID, "1", "Charlie", "35", "admin"},
			expectedRes: Resource{
				"_id":  textValue(testID),
				"_v":   numberValue(1),
				"name": textValue("Charlie"),
				"age":  numberValue(35),
				"tags": listValue("admin"),
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := testSchema.Resource(tt.record)
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
				return
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			} else if len(res) != len(tt.expectedRes) {
				t.Errorf("expected resource length %d, got %d", len(tt.expectedRes), len(res))
			} else {
				for k, v := range res {
					if !reflect.DeepEqual(v, tt.expectedRes[k]) {
						t.Errorf("expected %#v, got %#v", tt.expectedRes[k], v)
					}
				}
			}
		})
	}
}

func TestSchema_EdgeCases(t *testing.T) {
	t.Run("empty resource", func(t *testing.T) {
		schema := Schema{}
		resource := Resource{}

		rec := must(schema.Record(resource)).T(t)
		if len(rec) != 0 {
			t.Fatal("expected empty record for empty schema and resource, got:", rec)
		}
		res := must(schema.Resource(rec)).T(t)
		if len(res) != 0 {
			t.Fatal("expected empty resource for empty record, got:", res)
		}
	})

	t.Run("large numbers", func(t *testing.T) {
		schema := Schema{{Field: "big", Type: Number}}
		resource := Resource{"big": numberValue(math.MaxFloat64)}

		rec := must(schema.Record(resource)).T(t)
		want := Record{fmt.Sprintf("%g", math.MaxFloat64)}
		if !slices.Equal(rec, want) {
			t.Fatalf("expected %v, got %v", want, rec)
		}
	})

	t.Run("special characters in text", func(t *testing.T) {
		schema := Schema{{Field: "text", Type: Text}}
		resource := Resource{"text": textValue("特殊字符 日本語")}

		rec := must(schema.Record(resource)).T(t)
		want := Record{"特殊字符 日本語"}
		if !slices.Equal(rec, want) {
			t.Fatalf("expected %v, got %v", want, rec)
		}
	})
}
