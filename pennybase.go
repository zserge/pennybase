package pennybase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"iter"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type (
	Record []string
	Value  struct {
		Type   FieldType
		Number float64
		Text   string
		List   []string
	}
	Resource map[string]Value
)

type FieldType byte

const (
	Unknown FieldType = iota
	Number
	Text
	List
)

const namesFieldType = "UnknownNumberTextList"

var indicesFieldType = [...]int{0, 7, 13, 17, 21}

func (ft FieldType) String() string {
	if ft < Unknown || ft > List {
		return "Unknown"
	}
	return namesFieldType[indicesFieldType[ft]:indicesFieldType[ft+1]]
}

var FieldTypes = map[string]FieldType{
	"number": Number,
	"text":   Text,
	"list":   List,
}

type FieldSchema struct {
	Resource string
	Field    string
	Type     FieldType
	Min      float64
	Max      float64
	Regex    string
}

type Schema []FieldSchema

type DB interface {
	Create(r Record) error
	Update(r Record) error
	Get(id string) (Record, error)
	Delete(id string) error
	Iter() iter.Seq2[Record, error]
	Close() error
}

var (
	ID         = func() string { return rand.Text() }
	Salt       = func() string { return rand.Text() }
	HashPasswd = func(passwd, salt string) string {
		sum := sha256.Sum256([]byte(salt + passwd))
		return base32.StdEncoding.EncodeToString(sum[:])
	}
	SessionKey = Salt()
)

func (field FieldSchema) Validate(v Value) bool {
	if field.Type != v.Type {
		return false
	}
	switch field.Type {
	case Number:
		mn, mx := field.Min, field.Max
		if mn > mx {
			mx = math.MaxFloat64
		}
		return (mn == 0 && mx == 0) || (mn <= v.Number && v.Number <= mx)
	case Text:
		return field.Regex == "" || regexp.MustCompile(field.Regex).MatchString(v.Text)
	case List:
		return true
	}
	return false
}

func (s Schema) Record(res Resource) (rec Record, err error) {
	for i := range s {
		v := res[s[i].Field]
		if v.Type == Unknown {
			v = Value{Type: s[i].Type}
		}
		if !s[i].Validate(v) {
			return nil, fmt.Errorf("invalid field %q", s[i].Field)
		}
		switch s[i].Type {
		case Number:
			rec = append(rec, strconv.FormatFloat(v.Number, 'g', -1, 64))
		case Text:
			rec = append(rec, v.Text)
		case List:
			rec = append(rec, strings.Join(v.List, ","))
		}
	}
	return rec, nil
}

func (s Schema) Resource(rec Record) (Resource, error) {
	if len(rec) < len(s) {
		return nil, fmt.Errorf("record length %d is less than schema length %d", len(rec), len(s))
	}
	res := make(Resource, len(s))
	for i := range s {
		switch s[i].Type {
		case Number:
			n, err := strconv.ParseFloat(rec[i], 64)
			if err != nil {
				return nil, err
			}
			res[s[i].Field] = Value{Type: Number, Number: n}
		case Text:
			res[s[i].Field] = Value{Type: Text, Text: rec[i]}
		case List:
			v := Value{Type: List}
			if rec[i] != "" {
				v.List = strings.Split(rec[i], ",")
			}
			res[s[i].Field] = v
		default:
			return nil, fmt.Errorf("unknown field type %s", s[i].Type)
		}
	}
	return res, nil
}

type csvDB struct {
	mu      sync.Mutex
	f       *os.File
	w       *csv.Writer
	index   map[string]int64
	version map[string]int64
}

func NewCSVDB(path string) (*csvDB, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	db := &csvDB{f: f, w: csv.NewWriter(f), index: map[string]int64{}, version: map[string]int64{}}
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	for {
		pos := r.InputOffset()
		rec, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if len(rec) > 0 {
			db.index[rec[0]] = pos
			db.version[rec[0]], _ = strconv.ParseInt(rec[1], 10, 64)
		}
	}
	return db, nil
}

func (db *csvDB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.w.Flush()
	return db.f.Close()
}

func (db *csvDB) append(r Record) error {
	version, err := strconv.ParseInt(r[1], 10, 64)
	if err != nil {
		return err
	}
	pos, err := db.f.Seek(0, io.SeekEnd)
	if err == nil {
		err = db.w.Write(r)
	}
	if err != nil {
		return err
	}
	db.w.Flush()
	db.index[r[0]] = pos
	db.version[r[0]] = version
	return nil
}

func (db *csvDB) Create(r Record) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if r[0] == "" || r[1] != "1" || db.version[r[0]] != 0 {
		return errors.New("invalid record")
	}
	return db.append(r)
}

func (db *csvDB) Update(r Record) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if len(r) == 0 || r[1] != strconv.FormatInt(db.version[r[0]]+1, 10) {
		return errors.New("invalid record version")
	}
	return db.append(r)
}

func (db *csvDB) Delete(id string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.version[id] < 1 {
		return errors.New("record not found")
	}
	return db.append(Record{id, "0"})
}

func (db *csvDB) Get(id string) (Record, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.version[id] < 1 {
		return nil, errors.New("record not found")
	}
	offset, ok := db.index[id]
	if !ok {
		return nil, nil
	}
	if _, err := db.f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	r := csv.NewReader(db.f)
	rec, err := r.Read()
	if err != nil {
		return nil, err
	}
	if len(rec) > 0 && rec[0] != id {
		log.Println(rec)
		return nil, errors.New("corrupted index")
	}
	return rec, nil
}

func (db *csvDB) Iter() iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		db.mu.Lock()
		defer db.mu.Unlock()
		if _, err := db.f.Seek(0, io.SeekStart); err != nil {
			yield(nil, err)
			return
		}
		r := csv.NewReader(db.f)
		r.FieldsPerRecord = -1
		for {
			rec, err := r.Read()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				yield(nil, err)
				return
			} else if len(rec) < 2 {
				continue
			}
			id, version := rec[0], rec[1]
			if version == "0" || version != strconv.FormatInt(db.version[id], 10) {
				continue // deleted items or outdated versions
			}
			if !yield(rec, nil) {
				return
			}
		}
	}
}

func SignSession(username string) string {
	data := username + ":" + strconv.FormatInt(time.Now().Unix(), 10)
	h := sha256.New()
	h.Write([]byte(SessionKey))
	h.Write([]byte(data))
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	sig := base32.StdEncoding.EncodeToString(sum[:])[:16]
	return data + "." + sig
}

func VerifySession(session string) (string, bool) {
	i := strings.IndexByte(session, '.')
	if i < 0 {
		return "", false
	}
	data, sig := session[:i], session[i+1:]
	h := sha256.New()
	h.Write([]byte(SessionKey))
	h.Write([]byte(data))
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	expectedSig := base32.StdEncoding.EncodeToString(sum[:])[:16]
	if sig != expectedSig {
		return "", false
	}
	j := strings.IndexByte(data, ':')
	if j < 0 {
		return "", false
	}
	if ts, err := strconv.ParseInt(data[j+1:], 10, 64); err == nil {
		const secondsInDay = 24 * 60 * 60
		if time.Now().Unix()-ts < secondsInDay {
			return data[:j], true
		}
	}
	return "", false
}

type Store struct {
	Dir       string
	Schemas   map[string]Schema
	Resources map[string]DB
}

func NewStore(dir string) (*Store, error) {
	s := &Store{Dir: dir, Schemas: make(map[string]Schema), Resources: make(map[string]DB)}
	schemaDB, err := NewCSVDB(filepath.Join(s.Dir, "_schemas.csv"))
	if err != nil {
		return nil, err
	}
	for rec, err := range schemaDB.Iter() {
		if err != nil {
			return nil, err
		} else if len(rec) != 8 {
			return nil, fmt.Errorf("invalid schema record: %v", rec)
		}
		schema := FieldSchema{
			Resource: rec[2],
			Field:    rec[3],
			Type:     FieldTypes[rec[4]],
			Regex:    rec[7],
		}
		schema.Min, _ = strconv.ParseFloat(rec[5], 64)
		schema.Max, _ = strconv.ParseFloat(rec[6], 64)
		s.Schemas[schema.Resource] = append(s.Schemas[schema.Resource], schema)
		if _, ok := s.Resources[schema.Resource]; !ok {
			db, err := NewCSVDB(filepath.Join(s.Dir, schema.Resource+".csv"))
			if err != nil {
				return nil, err
			}
			s.Resources[schema.Resource] = db
		}
	}
	return s, nil
}

func (s *Store) Create(resource string, r Resource) (string, error) {
	db, ok := s.Resources[resource]
	if !ok {
		return "", fmt.Errorf("resource %s not found", resource)
	}
	newID := ID()
	r["_id"] = Value{Type: Text, Text: newID}
	r["_v"] = Value{Type: Number, Number: 1}
	rec, err := s.Schemas[resource].Record(r)
	if err != nil {
		return "", err
	}
	if err := db.Create(rec); err != nil {
		return "", err
	}
	return newID, nil
}

func (s *Store) Update(resource string, r Resource) error {
	db, ok := s.Resources[resource]
	if !ok {
		return fmt.Errorf("resource %s not found", resource)
	}
	orig, err := s.Get(resource, r["_id"].Text)
	if err != nil {
		return fmt.Errorf("record not found: %w", err)
	}
	for _, field := range s.Schemas[resource] {
		if _, ok := r[field.Field]; !ok {
			r[field.Field] = orig[field.Field]
		}
	}
	r["_v"] = Value{Type: Number, Number: orig["_v"].Number + 1}
	rec, err := s.Schemas[resource].Record(r)
	if err != nil {
		return err
	}
	return db.Update(rec)
}

func (s *Store) Delete(resource, id string) error {
	db, ok := s.Resources[resource]
	if !ok {
		return fmt.Errorf("resource %s not found", resource)
	}
	return db.Delete(id)
}

func (s *Store) Get(resource, id string) (Resource, error) {
	db, ok := s.Resources[resource]
	if !ok {
		return nil, fmt.Errorf("resource %s not found", resource)
	}
	rec, err := db.Get(id)
	if err != nil {
		return nil, err
	}
	if len(rec) < 2 {
		return nil, nil // record not found
	}
	return s.Schemas[resource].Resource(rec)
}

func (s *Store) List(resource, sortBy string) ([]Resource, error) {
	db, ok := s.Resources[resource]
	if !ok {
		return nil, fmt.Errorf("resource %s not found", resource)
	}
	res := []Resource{}
	for rec, err := range db.Iter() {
		if err != nil {
			return nil, err
		}
		if len(rec) < 2 {
			continue
		}
		r, err := s.Schemas[resource].Resource(rec)
		if err != nil {
			return res, err
		}
		res = append(res, r)
	}
	if sortBy != "" {
		sort.Slice(res, func(i, j int) bool {
			return res[j][sortBy].Type == Unknown ||
				res[i][sortBy].Text < res[j][sortBy].Text ||
				res[i][sortBy].Number < res[j][sortBy].Number
		})
	}
	return res, nil
}

func (s *Store) Close() error {
	for _, db := range s.Resources {
		if err := db.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Authenticate(r *http.Request) (Resource, error) {
	if cookie, err := r.Cookie("session"); err == nil {
		if username, ok := VerifySession(cookie.Value); ok {
			u, err := s.Get("_users", username)
			if err != nil {
				return nil, fmt.Errorf("users error: %w", err)
			}
			return u, nil
		}
	}
	if username, password, ok := r.BasicAuth(); ok {
		return s.AuthenticateBasic(username, password)
	}
	return nil, errors.New("unauthenticated")
}

func (s *Store) AuthenticateBasic(username, password string) (Resource, error) {
	u, err := s.Get("_users", username)
	if err != nil {
		return nil, fmt.Errorf("users error: %w", err)
	}
	if u["password"].Text != HashPasswd(password, u["salt"].Text) {
		return nil, errors.New("unauthenticated")
	}
	return u, nil
}

func (s *Store) Authorize(resource, id, action string, user Resource) error {
	permissions, err := s.List("_permissions", "")
	if err != nil {
		return fmt.Errorf("permissions error: %w", err)
	}
	for _, p := range permissions {
		if p["resource"].Text != resource || (p["action"].Text != "*" && p["action"].Text != action) {
			continue
		}
		if public := p["field"].Text == "" && p["role"].Text == ""; public {
			return nil
		}
		if user == nil {
			return errors.New("unauthenticated")
		}
		roles, userRole := user["roles"].List, p["role"].Text
		if userRole == "*" || slices.Contains(roles, userRole) {
			return nil
		}
		if id != "" {
			res, err := s.Get(resource, id)
			if err != nil {
				return err
			}
			user := user["_id"].Text
			userOrUsers := res[p["field"].Text]
			if userOrUsers.Text == user || slices.Contains(userOrUsers.List, user) {
				return nil
			}
		}
	}
	return errors.New("unauthorized")
}

type Event struct {
	Action string   `json:"action"`
	ID     string   `json:"id"`
	Data   Resource `json:"data"`
}

type Broker struct {
	// resource -> channels
	// map[string]map[chan Event]bool
	channels sync.Map
}

func (b *Broker) Subscribe(resource string, ch chan Event) {
	chanMap, ok := b.channels.Load(resource) // map[chan Event]bool
	if !ok {
		chanMap, _ = b.channels.LoadOrStore(resource, new(sync.Map))
	}
	chanMap.(*sync.Map).Store(ch, true)
}

func (b *Broker) Unsubscribe(resource string, ch chan Event) {
	chanMap, ok := b.channels.Load(resource)
	if !ok {
		return
	}
	chanMap.(*sync.Map).Delete(ch)
}

func (b *Broker) Publish(resource string, evt Event) {
	chanMap, ok := b.channels.Load(resource)
	if !ok {
		return
	}
	for ch := range chanMap.(*sync.Map).Range {
		select {
		case ch.(chan Event) <- evt:
		default:
		}
	}
}

type Hook func(trigger, resource string, user, r Resource) error

func nopHook(trigger, resource string, user, r Resource) error { return nil }

type Server struct {
	*http.ServeMux
	Store  *Store
	Broker *Broker
	Hook   Hook
}

var actions = map[string]string{"GET": "read", "POST": "create", "PUT": "update", "DELETE": "delete"}

type userResourceKey struct{}

var userResource userResourceKey

func ContextWithUser(ctx context.Context, user Resource) context.Context {
	return context.WithValue(ctx, userResource, user)
}

func UserFromContext(ctx context.Context) Resource {
	user, _ := ctx.Value(userResource).(Resource)
	return user
}

func NewServer(dataDir, tmplDir, staticDir string) (*Server, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return nil, err
	}
	s := &Server{Store: store, Broker: new(Broker), ServeMux: http.NewServeMux(), Hook: nopHook}
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			resource := r.PathValue("resource")
			action := actions[r.Method]
			user, _ := s.Store.Authenticate(r)
			if resource != "" && action != "" {
				if err := s.Store.Authorize(resource, r.PathValue("id"), action, user); err != nil {
					http.Error(w, err.Error(), http.StatusUnauthorized)
					return
				}
			}
			next(w, r.WithContext(ContextWithUser(r.Context(), user)))
		}
	}
	s.HandleFunc("GET /api/{resource}/", auth(s.handleList))
	s.HandleFunc("POST /api/{resource}/", auth(s.handleCreate))
	s.HandleFunc("GET /api/{resource}/{id}", auth(s.handleGet))
	s.HandleFunc("PUT /api/{resource}/{id}", auth(s.handleUpdate))
	s.HandleFunc("DELETE /api/{resource}/{id}", auth(s.handleDelete))
	s.HandleFunc("GET /api/events/{resource}", s.handleEvents)
	s.HandleFunc("POST /api/login", s.handleLogin)
	s.HandleFunc("POST /api/logout", s.handleLogout)
	if tmplDir != "" {
		tmpl, err := template.ParseGlob(filepath.Join(tmplDir, "*"))
		if err != nil {
			log.Fatal("Error parsing templates:", err)
		}
		for _, t := range tmpl.Templates() {
			if t.Name() == "index.html" {
				s.Handle("GET /", s.handleTemplate(t, "index.html"))
			}
			s.Handle("GET /"+t.Name(), s.handleTemplate(tmpl, t.Name()))
		}
	}
	if staticDir != "" {
		s.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	}

	return s, nil
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	res, err := s.Store.List(r.PathValue("resource"), r.FormValue("sort_by"))
	if err == nil {
		err = json.NewEncoder(w).Encode(res)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var res Resource
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resource := r.PathValue("resource")
	if err := s.Hook("create", resource, UserFromContext(r.Context()), res); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, err := s.Store.Create(resource, res)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Broker.Publish(resource, Event{Action: "created", ID: res["_id"].Text, Data: res})
	w.Header().Set("Location", path.Join("/api", resource, id))
	w.Header().Set("HX-Trigger", resource+"-changed")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	res, err := s.Store.Get(r.PathValue("resource"), r.PathValue("id"))
	if err == nil {
		if res == nil {
			http.NotFound(w, r)
			return
		}
		err = json.NewEncoder(w).Encode(res)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var res Resource
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resource := r.PathValue("resource")
	res["_id"] = Value{Type: Text, Text: r.PathValue("id")}
	if err := s.Hook("update", resource, UserFromContext(r.Context()), res); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Store.Update(resource, res); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Broker.Publish(resource, Event{Action: "updated", ID: res["_id"].Text, Data: res})
	w.Header().Set("HX-Trigger", fmt.Sprintf("%s-changed", resource))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	res, _ := s.Store.Get(r.PathValue("resource"), r.PathValue("id"))
	if err := s.Hook("delete", r.PathValue("resource"), UserFromContext(r.Context()), res); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Store.Delete(r.PathValue("resource"), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Broker.Publish(r.PathValue("resource"), Event{Action: "deleted", Data: res})
	w.Header().Set("HX-Trigger", fmt.Sprintf("%s-changed", r.PathValue("resource")))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	username, password := r.FormValue("username"), r.FormValue("password")
	if _, err := s.Store.AuthenticateBasic(username, password); err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    SignSession(username),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400, // 24 hours
	})
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTemplate(tmpl *template.Template, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := s.Store.Authenticate(r)
		data := map[string]any{
			"Store":   s.Store,
			"Request": r,
			"User":    user,
			"ID":      r.URL.Query().Get("_id"),
			"Authorize": func(resource, id, action string) bool {
				return s.Store.Authorize(resource, id, action, user) == nil
			},
		}
		if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
			log.Println("Error executing template:", name, err)
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	resource := r.PathValue("resource")
	user, err := s.Store.Authenticate(r)
	if err != nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	events := make(chan Event, 10)
	s.Broker.Subscribe(resource, events)
	defer s.Broker.Unsubscribe(resource, events)
	for {
		select {
		case e := <-events:
			if e.Action == "delete" || s.Store.Authorize(resource, e.ID, "read", user) == nil {
				data, _ := json.Marshal(e.Data)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Action, data)
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}
