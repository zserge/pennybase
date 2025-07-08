package pennybase

import (
	"errors"
	"testing"
)

func TestStoreCRUD(t *testing.T) {
	originalID := ID
	defer func() { ID = originalID }()

	tests := []struct {
		name      string
		operation func(*Store) error
		wantErr   bool
		postCheck func(*Store) error
	}{
		{
			name: "Create valid book",
			operation: func(s *Store) error {
				ID = func() string { return "test-id-1" }
				_, err := s.Create("books", Resource{
					"title":            textValue("The Go Programming Language"),
					"author":           textValue("Alan Donovan"),
					"publication_year": numberValue(2015),
					"genres":           listValue("Programming"),
					"isbn":             textValue("123-0123456789"),
				})
				return err
			},
			wantErr: false,
			postCheck: func(s *Store) error {
				res, err := s.Get("books", "test-id-1")
				if err != nil {
					return err
				}
				if res["title"].Text != "The Go Programming Language" || res["_v"].Number != 1 {
					return errors.New("created book mismatch")
				}
				return nil
			},
		},
		{
			name: "Create invalid book (missing title)",
			operation: func(s *Store) error {
				_, err := s.Create("books", Resource{
					"author":           textValue("Anonymous"),
					"publication_year": numberValue(2023),
					"genres":           listValue("Mystery"),
					"isbn":             textValue("999-9999999999"),
				})
				return err
			},
			wantErr: true,
		},
		{
			name: "Update book",
			operation: func(s *Store) error {
				ID = func() string { return "test-id-2" }
				_, err := s.Create("books", Resource{
					"title":            textValue("Original Title"),
					"author":           textValue("Author"),
					"publication_year": numberValue(2020),
					"genres":           listValue("Old"),
					"isbn":             textValue("111-1111111111"),
				})
				if err != nil {
					return err
				}
				return s.Update("books", Resource{
					"_id":              textValue("test-id-2"),
					"title":            textValue("Updated Title"),
					"author":           textValue("Author"),
					"publication_year": numberValue(2020),
					"genres":           listValue("New"),
					"isbn":             textValue("111-1111111111"),
				})
			},
			wantErr: false,
			postCheck: func(s *Store) error {
				res, err := s.Get("books", "test-id-2")
				if err != nil {
					return err
				}
				if res["title"].Text != "Updated Title" || res["_v"].Number != 2 {
					return errors.New("update failed")
				}
				return nil
			},
		},
		{
			name: "Delete book",
			operation: func(s *Store) error {
				ID = func() string { return "test-id-3" }
				_, err := s.Create("books", Resource{
					"title":            textValue("To Delete"),
					"author":           textValue("Author"),
					"publication_year": numberValue(2021),
					"genres":           listValue("Temp"),
					"isbn":             textValue("333-3333333333"),
				})
				if err != nil {
					return err
				}
				return s.Delete("books", "test-id-3")
			},
			wantErr: false,
			postCheck: func(s *Store) error {
				_, err := s.Get("books", "test-id-3")
				if err == nil {
					return errors.New("book not deleted")
				}
				return nil
			},
		},
		{
			name: "List books sorted",
			operation: func(s *Store) error {
				ID = func() string { return "book1" }
				_, err := s.Create("books", Resource{
					"title":            textValue("Book A"),
					"author":           textValue("Author A"),
					"publication_year": numberValue(2000),
					"genres":           listValue("Genre A"),
					"isbn":             textValue("111-0000000000"),
				})
				if err != nil {
					return err
				}
				ID = func() string { return "book2" }
				_, err = s.Create("books", Resource{
					"title":            textValue("Book B"),
					"author":           textValue("Author B"),
					"publication_year": numberValue(2020),
					"genres":           listValue("Genre B"),
					"isbn":             textValue("222-0000000000"),
				})
				return err
			},
			wantErr: false,
			postCheck: func(s *Store) error {
				books, err := s.List("books", "publication_year")
				if err != nil || len(books) != 2 {
					return errors.New("list failed")
				}
				if books[0]["publication_year"].Number != 2000.0 || books[1]["publication_year"].Number != 2020.0 {
					return errors.New("incorrect sort order")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := testData(t, "testdata/basic")
			store, err := NewStore(dir)
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			err = tt.operation(store)
			if (err != nil) != tt.wantErr {
				t.Errorf("got error %v, wantErr %v", err, tt.wantErr)
			}
			if tt.postCheck != nil {
				if err := tt.postCheck(store); err != nil {
					t.Errorf("postCheck failed: %v", err)
				}
			}
		})
	}
}
