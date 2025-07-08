package pennybase

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerREST(t *testing.T) {
	testDir := testData(t, filepath.Join("testdata", "rest"))
	s := must(NewServer(
		testDir,
		filepath.Join(testDir, "templates"),
		filepath.Join(testDir, "static"),
	)).T(t)
	defer s.Store.Close()

	ts := httptest.NewServer(s)
	defer ts.Close()

	tests := []struct {
		name     string
		method   string
		path     string
		body     any
		auth     [2]string // user, pass
		status   int
		validate func(*testing.T, *http.Response)
	}{
		// Public endpoints
		{
			name:   "List books unauthorized",
			method: http.MethodGet,
			path:   "/api/books/",
			status: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				var books []Resource
				must0(t, json.NewDecoder(resp.Body).Decode(&books))
				if len(books) != 2 {
					t.Errorf("Expected 2 books, got %d", len(books))
				}
			},
		},
		{
			name:   "Get static file",
			method: http.MethodGet,
			path:   "/static/test.txt",
			status: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				body := must(io.ReadAll(resp.Body)).T(t)
				if !bytes.Equal(body, []byte("Static text file\n")) {
					t.Errorf("Static file content mismatch: %s", body)
				}
			},
		},

		// Template rendering
		{
			name:   "Render books template",
			method: http.MethodGet,
			path:   "/books.html",
			status: http.StatusOK,
			validate: func(t *testing.T, resp *http.Response) {
				body, _ := io.ReadAll(resp.Body)
				if !strings.Contains(string(body), "The Go Programming Language") ||
					!strings.Contains(string(body), "1984") {
					t.Error(string(body))
					t.Error("Template missing book data")
				}
			},
		},

		// Authentication tests
		{
			name:   "Create book unauthenticated",
			method: http.MethodPost,
			path:   "/api/books/",
			body:   Resource{"title": textValue("New Book")},
			status: http.StatusUnauthorized,
		},
		{
			name:   "Create book invalid credentials",
			method: http.MethodPost,
			path:   "/api/books/",
			body:   Resource{"title": textValue("New Book")},
			auth:   [2]string{"user1", "wrongpass"},
			status: http.StatusUnauthorized,
		},

		// Authorized operations
		{
			name:   "Create book valid user",
			method: http.MethodPost,
			path:   "/api/books/",
			body:   Resource{"title": textValue("Valid Book"), "author": textValue("Unknown Author"), "year": numberValue(2023)},
			auth:   [2]string{"user1", "user1pass"},
			status: http.StatusCreated,
			validate: func(t *testing.T, resp *http.Response) {
				loc := resp.Header.Get("Location")
				if !strings.HasPrefix(loc, "/api/books/") {
					t.Error("Missing Location header")
				}
			},
		},
		{
			name:   "Update book unauthorized",
			method: http.MethodPut,
			path:   "/api/books/book1",
			body:   Resource{"title": textValue("Updated Title")},
			auth:   [2]string{"user1", "user1pass"},
			status: http.StatusUnauthorized,
		},

		// Admin operations
		{
			name:   "Delete book as admin",
			method: http.MethodDelete,
			path:   "/api/books/book2",
			auth:   [2]string{"admin", "admin123"},
			status: http.StatusOK,
		},

		// Validation tests
		{
			name:   "Create invalid book",
			method: http.MethodPost,
			path:   "/api/books/",
			body:   Resource{"title": textValue("Book 123"), "year": numberValue(3000)},
			auth:   [2]string{"user1", "user1pass"},
			status: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != nil {
				b := must(json.Marshal(tc.body)).T(t)
				body = bytes.NewReader(b)
			}

			u := must(url.JoinPath(ts.URL, tc.path)).T(t)
			req := must(http.NewRequest(tc.method, u, body)).T(t)
			if tc.auth[0] != "" {
				req.SetBasicAuth(tc.auth[0], tc.auth[1])
			}

			resp := must(http.DefaultClient.Do(req)).T(t)
			defer resp.Body.Close()

			if resp.StatusCode != tc.status {
				t.Errorf("Expected status %d, got %d", tc.status, resp.StatusCode)
			}

			if tc.validate != nil {
				tc.validate(t, resp)
			}
		})
	}
}

func TestServerTemplate(t *testing.T) {
	dir := testData(t, filepath.Join("testdata", "rest"))
	s := must(NewServer(dir, filepath.Join(dir, "templates"), "" /*staticDir*/)).T(t)
	req := httptest.NewRequest(http.MethodGet, "/books.html", nil)
	w := httptest.NewRecorder()
	s.ServeMux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 OK, got %d", w.Code)
	}
	gotBody := w.Body.String()
	if !strings.Contains(gotBody, "<div class=\"book\">The Go Programming Language (2015)</div>") {
		t.Errorf("Got unexpected body: %v", gotBody)
	}
	if !strings.Contains(gotBody, "<div class=\"book\">1984 (1949)</div>") {
		t.Errorf("Got unexpected body: %v", gotBody)
	}
}

func TestServerStaticFiles(t *testing.T) {
	dir := testData(t, filepath.Join("testdata", "rest"))
	s := must(NewServer(dir, "" /*tmplDir*/, filepath.Join(dir, "static"))).T(t)
	req := httptest.NewRequest(http.MethodGet, "/static/test.txt", nil)
	w := httptest.NewRecorder()
	s.ServeMux.ServeHTTP(w, req)
	if gotBody := w.Body.String(); gotBody != "Static text file\n" {
		t.Errorf("Static file serving failed: %v", gotBody)
	}
}
